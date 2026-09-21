// Package telegram exposes Foya conversations through a Telegram bot.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	foyachannel "github.com/freesoulcode/foya/internal/channel"
	"github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/interaction"
)

const (
	maxMessageRunes = 4000
)

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
	bindings *foyachannel.ConversationBindingStore
	pairings *foyachannel.ConversationPairingStore
	api      API
	logger   *log.Logger
	username string
	core     *foyachannel.ChannelCore

	mu sync.Mutex
	wg sync.WaitGroup
}

func New(
	config Config,
	runtime foyachannel.Runtime,
	bindings *foyachannel.ConversationBindingStore,
	pairings *foyachannel.ConversationPairingStore,
	api API,
	logger *log.Logger,
) (*Bot, error) {
	if runtime == nil {
		return nil, errors.New("Telegram bot backend is required")
	}
	if bindings == nil {
		return nil, errors.New("Telegram conversation binding store is required")
	}
	if pairings == nil {
		return nil, errors.New("Telegram conversation pairing store is required")
	}
	if api == nil {
		return nil, errors.New("Telegram API client is required")
	}
	if strings.TrimSpace(config.ChannelID) == "" {
		return nil, errors.New("Telegram channel ID is required")
	}
	if config.ApprovalMode == "" {
		config.ApprovalMode = interaction.ModeAuto
	}
	if config.ApprovalMode != interaction.ModeAuto &&
		config.ApprovalMode != interaction.ModeFullAccess {
		return nil, errors.New("Telegram bot approval mode must be auto or full_access")
	}
	config.AllowedUsers = cleanIDs(config.AllowedUsers)
	config.AllowedChats = cleanIDs(config.AllowedChats)
	if logger == nil {
		logger = log.Default()
	}
	bot := &Bot{
		config:   config,
		backend:  runtime,
		bindings: bindings,
		pairings: pairings,
		api:      api,
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
		Locale: config.Locale,
		Localize: func(key string, args ...any) string {
			return localizedMessage(config.Locale, key, args...)
		},
		Logger: logger,
	}, runtime, bindings, pairings, bot)
	if err != nil {
		return nil, err
	}
	bot.core = core
	return bot, nil
}

func (b *Bot) Run(ctx context.Context) error {
	b.core.SetRunContext(ctx)
	me, err := b.api.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("get Telegram bot identity: %w", err)
	}
	b.username = strings.TrimPrefix(strings.TrimSpace(me.Username), "@")
	var offset int64
	for {
		updates, err := b.api.GetUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			b.logger.Printf("Telegram polling failed: %v", err)
			select {
			case <-ctx.Done():
				break
			case <-time.After(time.Second):
				continue
			}
		}
		for _, update := range updates {
			if update.ID >= offset {
				offset = update.ID + 1
			}
			if update.Message != nil {
				b.enqueue(ctx, *update.Message)
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	b.core.Wait()
	b.wg.Wait()
	return nil
}

func (b *Bot) enqueue(ctx context.Context, message Message) {
	if message.ID == 0 || message.Chat.ID == 0 || !b.addressed(message) {
		return
	}
	_, err := b.core.Enqueue(ctx, b.incoming(message))
	if err != nil && !errors.Is(err, context.Canceled) {
		b.logger.Printf("enqueue Telegram message: %v", err)
	}
}

func (b *Bot) processMessage(ctx context.Context, message Message) error {
	return b.core.Process(ctx, b.incoming(message))
}

func (b *Bot) incoming(message Message) foyachannel.IncomingMessage {
	chatID := strconv.FormatInt(message.Chat.ID, 10)
	return foyachannel.IncomingMessage{
		ConversationKey: conversationKey(chatID),
		Kind:            message.Chat.Type,
		ExternalID:      chatID,
		DisplayName:     conversationDisplayName(message),
		Content:         b.cleanContent(message),
		Source:          message,
	}
}

func (b *Bot) Allowed(incoming foyachannel.IncomingMessage) bool {
	message, ok := incoming.Source.(Message)
	return ok && b.allowed(message)
}

func (b *Bot) Send(
	ctx context.Context,
	incoming foyachannel.IncomingMessage,
	text string,
	_ bool,
) error {
	message, ok := incoming.Source.(Message)
	if !ok {
		return errors.New("Telegram message source is required")
	}
	return b.sendText(ctx, message.Chat.ID, text)
}

func (b *Bot) Attachments(
	context.Context,
	string,
	foyachannel.IncomingMessage,
) ([]conversation.AttachmentRef, error) {
	return nil, nil
}

func (b *Bot) sessionForChat(ctx context.Context, chatID string) (string, error) {
	key := conversationKey(chatID)
	sessionID, err := b.bindings.Get(ctx, b.config.ChannelID, key)
	if err != nil {
		return "", err
	}
	if sessionID != "" {
		for _, item := range b.backend.ListSessions() {
			if item.ID == sessionID {
				return sessionID, nil
			}
		}
		if err := b.bindings.UnbindSession(ctx, sessionID); err != nil {
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
	if err := b.bindings.Bind(
		ctx,
		b.config.ChannelID,
		conversationKey(chatID),
		created.ID,
	); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (b *Bot) allowed(message Message) bool {
	if b.config.AllowAll {
		return true
	}
	chatID := strconv.FormatInt(message.Chat.ID, 10)
	if message.Chat.Type == "group" || message.Chat.Type == "supergroup" {
		if contains(b.config.AllowedChats, chatID) {
			return true
		}
		if message.From == nil {
			return false
		}
		userID := strconv.FormatInt(message.From.ID, 10)
		username := strings.TrimPrefix(message.From.Username, "@")
		return contains(b.config.AllowedUsers, userID) ||
			(username != "" && contains(b.config.AllowedUsers, username))
	}
	if message.From == nil {
		return false
	}
	userID := strconv.FormatInt(message.From.ID, 10)
	username := strings.TrimPrefix(message.From.Username, "@")
	return contains(b.config.AllowedUsers, userID) ||
		(username != "" && contains(b.config.AllowedUsers, username))
}

func (b *Bot) addressed(message Message) bool {
	if message.Chat.Type != "group" && message.Chat.Type != "supergroup" {
		return true
	}
	content := strings.TrimSpace(message.Text)
	if strings.HasPrefix(content, "/") {
		command := strings.Fields(content)[0]
		_, target, targeted := strings.Cut(command, "@")
		return !targeted || strings.EqualFold(target, b.username)
	}
	return b.username != "" &&
		strings.Contains(strings.ToLower(content), "@"+strings.ToLower(b.username))
}

func (b *Bot) cleanContent(message Message) string {
	content := message.Text
	if content == "" {
		content = message.Caption
	}
	if b.username != "" {
		content = strings.ReplaceAll(content, "@"+b.username, "")
		content = strings.ReplaceAll(content, "@"+strings.ToLower(b.username), "")
	}
	fields := strings.Fields(content)
	if len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
		command, _, _ := strings.Cut(fields[0], "@")
		fields[0] = command
		content = strings.Join(fields, " ")
	}
	return strings.TrimSpace(content)
}

func (b *Bot) replyError(ctx context.Context, chatID int64, err error) error {
	sendErr := b.sendText(ctx, chatID, localizedMessage(b.config.Locale, "request_failed", err))
	return errors.Join(err, sendErr)
}

func (b *Bot) sendText(ctx context.Context, chatID int64, text string) error {
	for _, chunk := range splitText(text, maxMessageRunes) {
		if err := b.api.SendMessage(ctx, chatID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func conversationKey(chatID string) string {
	return "chat:" + strings.TrimSpace(chatID)
}

func conversationDisplayName(message Message) string {
	if message.Chat.Title != "" {
		return message.Chat.Title
	}
	if message.Chat.Username != "" {
		return "@" + message.Chat.Username
	}
	name := strings.TrimSpace(message.Chat.FirstName + " " + message.Chat.LastName)
	if name != "" {
		return name
	}
	if message.From != nil && message.From.Username != "" {
		return "@" + message.From.Username
	}
	return strconv.FormatInt(message.Chat.ID, 10)
}

func (b *Bot) observeConversation(ctx context.Context, message Message) error {
	chatID := strconv.FormatInt(message.Chat.ID, 10)
	return b.bindings.Observe(
		ctx,
		b.config.ChannelID,
		conversationKey(chatID),
		message.Chat.Type,
		chatID,
		conversationDisplayName(message),
	)
}

func cleanIDs(items []string) []string {
	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimPrefix(strings.TrimSpace(item), "@")
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

func contains(items []string, target string) bool {
	target = strings.TrimPrefix(strings.TrimSpace(target), "@")
	for _, item := range items {
		if strings.EqualFold(item, target) {
			return true
		}
	}
	return false
}

func splitText(text string, limit int) []string {
	if limit <= 0 {
		return []string{text}
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}
	parts := make([]string, 0, (len(runes)+limit-1)/limit)
	for len(runes) > 0 {
		size := min(limit, len(runes))
		parts = append(parts, string(runes[:size]))
		runes = runes[size:]
	}
	return parts
}
