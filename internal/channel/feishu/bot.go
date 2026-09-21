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

	foyachannel "github.com/freesoulcode/foya/internal/channel"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"

	"github.com/larksuite/oapi-sdk-go/v3/channel/types"
)

const acknowledgementReaction = "OK"

type Config struct {
	ChannelID    string
	ConnectionID string
	Model        string
	ProjectID    string
	ApprovalMode interaction.Mode
	Locale       string
	AllowedUsers []string
	AllowedChats []string
	AllowAll     bool
}

type Bot struct {
	config   Config
	backend  foyachannel.Runtime
	channel  Channel
	store    *foyachannel.ConversationBindingStore
	pairings *foyachannel.ConversationPairingStore
	logger   *log.Logger
	core     *foyachannel.ChannelCore

	mu      sync.Mutex
	runCtx  context.Context
	started bool
	wg      sync.WaitGroup
}

func New(
	config Config,
	runtime foyachannel.Runtime,
	store *foyachannel.ConversationBindingStore,
	pairings *foyachannel.ConversationPairingStore,
	channel Channel,
	logger *log.Logger,
) (*Bot, error) {
	if runtime == nil {
		return nil, errors.New("Feishu bot backend is required")
	}
	if store == nil {
		return nil, errors.New("Feishu conversation binding store is required")
	}
	if pairings == nil {
		return nil, errors.New("Feishu conversation pairing store is required")
	}
	if channel == nil {
		return nil, errors.New("Feishu channel is required")
	}
	if strings.TrimSpace(config.ChannelID) == "" {
		return nil, errors.New("Feishu channel ID is required")
	}
	if config.ApprovalMode == "" {
		config.ApprovalMode = interaction.ModeAuto
	}
	if config.Locale != "en-US" && config.Locale != "zh-CN" {
		config.Locale = "zh-CN"
	}
	if config.ApprovalMode != interaction.ModeAuto &&
		config.ApprovalMode != interaction.ModeFullAccess {
		return nil, errors.New("Feishu bot approval mode must be auto or full_access")
	}
	config.AllowedUsers = cleanIDs(config.AllowedUsers)
	config.AllowedChats = cleanIDs(config.AllowedChats)
	if logger == nil {
		logger = log.Default()
	}
	bot := &Bot{
		config:   config,
		backend:  runtime,
		channel:  channel,
		store:    store,
		pairings: pairings,
		logger:   logger,
	}
	core, err := foyachannel.NewChannelCore(foyachannel.CoreConfig{
		ChannelID: config.ChannelID,
		CreateOptions: conversation.CreateOptions{
			ConnectionID: config.ConnectionID,
			Model:        config.Model,
			ProjectID:    config.ProjectID,
			ApprovalMode: string(config.ApprovalMode),
		},
		Locale:           config.Locale,
		ResponseMarkdown: true,
		Localize: func(key string, args ...any) string {
			return localizedMessage(config.Locale, key, args...)
		},
		Logger: logger,
	}, runtime, store, pairings, bot)
	if err != nil {
		return nil, err
	}
	bot.core = core
	return bot, nil
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
	b.core.SetRunContext(runCtx)

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
	b.core.Wait()
	b.wg.Wait()
	b.mu.Lock()
	b.runCtx = nil
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
	queued, err := b.core.Enqueue(ctx, b.incoming(incoming))
	if err != nil || !queued {
		if b.incoming(incoming).Content == "/stop" {
			b.acknowledge(ctx, incoming)
		}
		return err
	}
	b.acknowledge(ctx, incoming)
	return nil
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

func (b *Bot) processMessage(ctx context.Context, incoming *types.NormalizedMessage) error {
	return b.core.Process(ctx, b.incoming(incoming))
}

func (b *Bot) incoming(incoming *types.NormalizedMessage) foyachannel.IncomingMessage {
	return foyachannel.IncomingMessage{
		ConversationKey: conversationKey(incoming.ChatID),
		Kind:            incoming.ChatType,
		ExternalID:      incoming.ChatID,
		DisplayName:     conversationDisplayName(incoming),
		Content:         cleanContent(incoming),
		Source:          incoming,
	}
}

func (b *Bot) Allowed(message foyachannel.IncomingMessage) bool {
	incoming, _ := message.Source.(*types.NormalizedMessage)
	return incoming != nil && b.allowed(incoming)
}

func (b *Bot) Send(
	ctx context.Context,
	message foyachannel.IncomingMessage,
	text string,
	markdown bool,
) error {
	incoming, _ := message.Source.(*types.NormalizedMessage)
	if incoming == nil {
		return errors.New("Feishu message source is required")
	}
	if markdown {
		return b.sendMarkdown(ctx, incoming, text)
	}
	return b.sendText(ctx, incoming, text)
}

func (b *Bot) Attachments(
	ctx context.Context,
	sessionID string,
	message foyachannel.IncomingMessage,
) ([]conversation.AttachmentRef, error) {
	incoming, _ := message.Source.(*types.NormalizedMessage)
	if incoming == nil {
		return nil, errors.New("Feishu message source is required")
	}
	return b.imageAttachments(ctx, sessionID, incoming)
}

func (b *Bot) sessionForChat(ctx context.Context, chatID string) (string, error) {
	sessionID, err := b.store.Get(ctx, b.config.ChannelID, conversationKey(chatID))
	if err != nil {
		return "", err
	}
	if sessionID != "" {
		for _, item := range b.backend.ListSessions() {
			if item.ID == sessionID {
				return sessionID, nil
			}
		}
		if err := b.store.UnbindSession(ctx, sessionID); err != nil {
			return "", err
		}
	}
	return "", foyachannel.ErrConversationUnbound
}

func (b *Bot) createSession(ctx context.Context, chatID string) (string, error) {
	created, err := b.backend.CreateSession(conversation.CreateOptions{
		ConnectionID: b.config.ConnectionID,
		Model:        b.config.Model,
		ProjectID:    b.config.ProjectID,
		ApprovalMode: string(b.config.ApprovalMode),
	})
	if err != nil {
		return "", err
	}
	if err := b.store.Bind(ctx, b.config.ChannelID, conversationKey(chatID), created.ID); err != nil {
		return "", err
	}
	return created.ID, nil
}

func conversationKey(chatID string) string {
	return "chat:" + strings.TrimSpace(chatID)
}

func conversationDisplayName(incoming *types.NormalizedMessage) string {
	if incoming.ChatType == "p2p" && incoming.UserID != "" {
		return incoming.UserID
	}
	return incoming.ChatID
}

func (b *Bot) observeConversation(
	ctx context.Context,
	incoming *types.NormalizedMessage,
) error {
	return b.store.Observe(
		ctx,
		b.config.ChannelID,
		conversationKey(incoming.ChatID),
		incoming.ChatType,
		incoming.ChatID,
		conversationDisplayName(incoming),
	)
}

func (b *Bot) imageAttachments(
	ctx context.Context,
	sessionID string,
	incoming *types.NormalizedMessage,
) ([]conversation.AttachmentRef, error) {
	for _, resource := range incoming.Resources {
		if resource.Type != "image" {
			return nil, errors.New(localizedMessage(
				b.config.Locale,
				"unsupported_attachment",
				resource.Type,
			))
		}
	}
	attachments := make([]conversation.AttachmentRef, 0, len(incoming.Resources))
	for index, resource := range incoming.Resources {
		data, err := b.channel.DownloadFile(ctx, resource.FileKey, "image")
		if err != nil {
			return nil, errors.New(localizedMessage(b.config.Locale, "download_image_failed", err))
		}
		name := strings.TrimSpace(resource.FileName)
		if name == "" {
			name = fmt.Sprintf("feishu-image-%d", index+1)
		}
		ref, err := b.backend.PutImage(ctx, sessionID, filepath.Base(name), bytes.NewReader(data))
		if err != nil {
			return nil, errors.New(localizedMessage(b.config.Locale, "import_image_failed", err))
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
		return contains(b.config.AllowedChats, incoming.ChatID) ||
			contains(b.config.AllowedUsers, incoming.UserID)
	}
	return contains(b.config.AllowedUsers, incoming.UserID)
}

func (b *Bot) replyError(ctx context.Context, incoming *types.NormalizedMessage, err error) error {
	sendErr := b.sendText(ctx, incoming, localizedMessage(b.config.Locale, "request_failed", err))
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
