// Package feishu exposes Foya conversations through a Feishu bot.
package feishu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
	foyachannel "github.com/freesoulcode/foya/internal/channel"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/question"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/larksuite/oapi-sdk-go/v3/channel/types"
)

const workerQueueSize = 32
const acknowledgementReaction = "OK"

type Config struct {
	ConnectionID string
	Model        string
	ProjectID    string
	ApprovalMode approval.Mode
	SessionPath  string
	AllowedUsers []string
	AllowedChats []string
	AllowAll     bool
}

type Bot struct {
	config  Config
	backend foyachannel.Runtime
	channel Channel
	store   *foyachannel.SessionStore
	logger  *log.Logger

	mu      sync.Mutex
	runCtx  context.Context
	started bool
	workers map[string]chan *types.NormalizedMessage
	wg      sync.WaitGroup
}

func New(config Config, runtime foyachannel.Runtime, channel Channel, logger *log.Logger) (*Bot, error) {
	if runtime == nil {
		return nil, errors.New("Feishu bot backend is required")
	}
	if channel == nil {
		return nil, errors.New("Feishu channel is required")
	}
	if config.SessionPath == "" {
		return nil, errors.New("Feishu session path is required")
	}
	if config.ApprovalMode == "" {
		config.ApprovalMode = approval.ModeAuto
	}
	if config.ApprovalMode != approval.ModeAuto && config.ApprovalMode != approval.ModeFullAccess {
		return nil, errors.New("Feishu bot approval mode must be auto or full_access")
	}
	config.AllowedUsers = cleanIDs(config.AllowedUsers)
	config.AllowedChats = cleanIDs(config.AllowedChats)
	if !config.AllowAll && len(config.AllowedUsers) == 0 && len(config.AllowedChats) == 0 {
		return nil, errors.New("configure at least one allowed user or chat, or explicitly enable allow-all")
	}
	store, err := foyachannel.OpenSessionStore(config.SessionPath)
	if err != nil {
		return nil, fmt.Errorf("open Feishu session store: %w", err)
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Bot{
		config:  config,
		backend: runtime,
		channel: channel,
		store:   store,
		logger:  logger,
		workers: make(map[string]chan *types.NormalizedMessage),
	}, nil
}

func (b *Bot) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		cancel()
		return errors.New("Feishu bot cannot be started more than once")
	}
	b.started = true
	b.runCtx = runCtx
	b.mu.Unlock()

	b.channel.OnMessage(b.enqueue)
	b.channel.OnReject(func(_ context.Context, rejected *types.RejectEvent) error {
		b.logger.Printf(
			"Feishu message rejected: reason=%s sender=%s chat=%s",
			rejected.Reason,
			rejected.SenderID,
			rejected.ChatID,
		)
		return nil
	})
	b.channel.OnReady(func() {
		b.logger.Print("Feishu bot connected")
	})
	b.channel.OnError(func(err error) {
		b.logger.Printf("Feishu channel error: %v", err)
	})
	b.channel.OnReconnecting(func() {
		b.logger.Print("Feishu bot reconnecting")
	})
	b.channel.OnReconnected(func() {
		b.logger.Print("Feishu bot reconnected")
	})

	startResult := make(chan error, 1)
	go func() {
		startResult <- b.channel.Start(runCtx)
	}()

	var result error
	select {
	case result = <-startResult:
	case <-ctx.Done():
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		stopErr := b.channel.Stop(stopCtx)
		stopCancel()
		result = <-startResult
		if stopErr != nil {
			result = errors.Join(result, stopErr)
		}
	}

	cancel()
	b.wg.Wait()
	b.mu.Lock()
	b.runCtx = nil
	b.workers = make(map[string]chan *types.NormalizedMessage)
	b.mu.Unlock()
	if errors.Is(result, context.Canceled) {
		return nil
	}
	return result
}

func (b *Bot) enqueue(ctx context.Context, incoming *types.NormalizedMessage) error {
	if incoming == nil || incoming.ChatID == "" || incoming.MessageID == "" {
		return nil
	}
	if !b.allowed(incoming) {
		b.logger.Printf(
			"Feishu message rejected: reason=not_allowed sender=%s chat=%s",
			incoming.UserID,
			incoming.ChatID,
		)
		return nil
	}

	content := cleanContent(incoming)
	if content == "/stop" {
		if sessionID := b.store.Get(incoming.ChatID); sessionID != "" {
			b.backend.CancelTurn(sessionID)
			b.acknowledge(ctx, incoming)
			return b.sendText(ctx, incoming, "已停止当前任务。")
		}
		b.acknowledge(ctx, incoming)
		return b.sendText(ctx, incoming, "当前没有可停止的任务。")
	}

	b.mu.Lock()
	runCtx := b.runCtx
	if runCtx == nil {
		runCtx = ctx
	}
	worker, ok := b.workers[incoming.ChatID]
	if !ok {
		worker = make(chan *types.NormalizedMessage, workerQueueSize)
		b.workers[incoming.ChatID] = worker
		b.wg.Add(1)
		go b.runWorker(runCtx, incoming.ChatID, worker)
	}
	b.mu.Unlock()

	cloned := cloneMessage(incoming)
	select {
	case worker <- cloned:
		b.acknowledge(runCtx, incoming)
		return nil
	case <-runCtx.Done():
		return runCtx.Err()
	default:
		return b.sendText(ctx, incoming, "消息队列已满，请稍后重试。")
	}
}

func (b *Bot) acknowledge(ctx context.Context, incoming *types.NormalizedMessage) {
	messageID := incoming.MessageID
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		reactionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := b.channel.React(reactionCtx, messageID, acknowledgementReaction); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			b.logger.Printf("Feishu acknowledgement reaction failed: message=%s error=%v", messageID, err)
		}
	}()
}

func (b *Bot) runWorker(ctx context.Context, chatID string, input <-chan *types.NormalizedMessage) {
	defer b.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case incoming := <-input:
			if incoming == nil {
				continue
			}
			if err := b.processMessage(ctx, incoming); err != nil && !errors.Is(err, context.Canceled) {
				b.logger.Printf("Feishu message failed: chat=%s message=%s error=%v", chatID, incoming.MessageID, err)
			}
		}
	}
}

func (b *Bot) processMessage(ctx context.Context, incoming *types.NormalizedMessage) error {
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	content := cleanContent(incoming)
	if content == "/new" {
		if _, err := b.createSession(incoming.ChatID); err != nil {
			return b.replyError(turnCtx, incoming, err)
		}
		return b.sendText(turnCtx, incoming, "已开启新会话。")
	}

	sessionID, err := b.sessionForChat(incoming.ChatID)
	if err != nil {
		return b.replyError(turnCtx, incoming, err)
	}
	attachments, err := b.imageAttachments(turnCtx, sessionID, incoming)
	if err != nil {
		return b.replyError(turnCtx, incoming, err)
	}
	if content == "" && len(attachments) == 0 {
		return b.sendText(turnCtx, incoming, "暂时只支持文本和图片消息。")
	}

	events := b.backend.Subscribe(turnCtx, sessionID)
	if err := b.backend.SubmitChatInput(turnCtx, sessionID, message.UserInput{
		Text:        content,
		Attachments: attachments,
	}); err != nil {
		return b.replyError(turnCtx, incoming, err)
	}

	var lastAssistant string
	var finalAssistant string
	var responseErr error
	for {
		select {
		case <-turnCtx.Done():
			return turnCtx.Err()
		case item, ok := <-events:
			if !ok {
				return errors.New("Foya event stream closed before turn completion")
			}
			switch item.Kind {
			case event.KindMessageEnd:
				item, ok := item.Payload.(message.Message)
				if !ok || item.Role != message.RoleAssistant {
					continue
				}
				if item.Content != "" {
					lastAssistant = item.Content
				}
				if len(item.ToolCalls) == 0 {
					finalAssistant = item.Content
				}
			case event.KindApprovalReq:
				request, ok := item.Payload.(approval.Request)
				if ok {
					_ = b.backend.ResolveApproval(request.ID, string(approval.DecisionDenied))
				}
			case event.KindQuestionRequested:
				batch, ok := item.Payload.(question.Batch)
				if ok {
					_ = b.backend.CancelQuestions(sessionID, batch.ID)
				}
			case event.KindError:
				responseErr = fmt.Errorf("%v", item.Payload)
			case event.KindTurnComplete:
				response := finalAssistant
				if response == "" {
					response = lastAssistant
				}
				if responseErr != nil {
					if response != "" {
						response += "\n\n> "
					}
					response += responseErr.Error()
				}
				if response == "" {
					response = "任务已结束，但没有生成文本回复。"
				}
				return b.sendMarkdown(turnCtx, incoming, response)
			}
		}
	}
}

func (b *Bot) sessionForChat(chatID string) (string, error) {
	if sessionID := b.store.Get(chatID); sessionID != "" {
		for _, item := range b.backend.ListSessions() {
			if item.ID == sessionID {
				return sessionID, nil
			}
		}
	}
	return b.createSession(chatID)
}

func (b *Bot) createSession(chatID string) (string, error) {
	created, err := b.backend.CreateSession(session.CreateOptions{
		ConnectionID: b.config.ConnectionID,
		Model:        b.config.Model,
		ProjectID:    b.config.ProjectID,
		ApprovalMode: string(b.config.ApprovalMode),
	})
	if err != nil {
		return "", err
	}
	if err := b.store.Set(chatID, created.ID); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (b *Bot) imageAttachments(
	ctx context.Context,
	sessionID string,
	incoming *types.NormalizedMessage,
) ([]message.AttachmentRef, error) {
	for _, resource := range incoming.Resources {
		if resource.Type != "image" {
			return nil, fmt.Errorf("暂时不支持 %s 类型的附件", resource.Type)
		}
	}
	attachments := make([]message.AttachmentRef, 0, len(incoming.Resources))
	for index, resource := range incoming.Resources {
		data, err := b.channel.DownloadFile(ctx, resource.FileKey, "image")
		if err != nil {
			return nil, fmt.Errorf("下载图片失败: %w", err)
		}
		name := strings.TrimSpace(resource.FileName)
		if name == "" {
			name = fmt.Sprintf("feishu-image-%d", index+1)
		}
		ref, err := b.backend.PutImage(ctx, sessionID, filepath.Base(name), bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("导入图片失败: %w", err)
		}
		attachments = append(attachments, ref)
	}
	return attachments, nil
}

func (b *Bot) allowed(incoming *types.NormalizedMessage) bool {
	if b.config.AllowAll {
		return true
	}
	if incoming.ChatType == "group" {
		return contains(b.config.AllowedChats, incoming.ChatID)
	}
	return contains(b.config.AllowedUsers, incoming.UserID)
}

func (b *Bot) replyError(ctx context.Context, incoming *types.NormalizedMessage, err error) error {
	sendErr := b.sendText(ctx, incoming, "请求失败："+err.Error())
	return errors.Join(err, sendErr)
}

func (b *Bot) sendText(ctx context.Context, incoming *types.NormalizedMessage, text string) error {
	_, err := b.channel.Send(ctx, &types.SendInput{
		ChatID:         incoming.ChatID,
		ReplyMessageID: incoming.MessageID,
		Text:           text,
	})
	return err
}

func (b *Bot) sendMarkdown(ctx context.Context, incoming *types.NormalizedMessage, markdown string) error {
	_, err := b.channel.Send(ctx, &types.SendInput{
		ChatID:         incoming.ChatID,
		ReplyMessageID: incoming.MessageID,
		Markdown:       markdown,
	})
	return err
}

func cleanContent(incoming *types.NormalizedMessage) string {
	content := incoming.Content
	for _, mention := range incoming.Mentions {
		if mention.IsBot && mention.Key != "" {
			content = strings.ReplaceAll(content, mention.Key, "")
		}
	}
	for _, resource := range incoming.Resources {
		if resource.Type == "image" {
			content = strings.ReplaceAll(content, "![image]("+resource.FileKey+")", "")
		}
	}
	return strings.TrimSpace(content)
}

func cloneMessage(source *types.NormalizedMessage) *types.NormalizedMessage {
	cloned := *source
	cloned.Mentions = append([]types.Mention(nil), source.Mentions...)
	cloned.Resources = append([]types.Resource(nil), source.Resources...)
	return &cloned
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func cleanIDs(items []string) []string {
	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}
