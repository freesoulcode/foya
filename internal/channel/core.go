package channel

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/interaction"
)

const defaultWorkerQueueSize = 32

// IncomingMessage is the platform-neutral representation consumed by ChannelCore.
// Provider adapters retain the original payload in Source when they need it for replies.
type IncomingMessage struct {
	ConversationKey string
	Kind            string
	ExternalID      string
	DisplayName     string
	Content         string
	Source          any
}

// CoreProvider supplies the narrow platform-specific operations required by ChannelCore.
type CoreProvider interface {
	Allowed(IncomingMessage) bool
	Send(context.Context, IncomingMessage, string, bool) error
	Attachments(context.Context, string, IncomingMessage) ([]conversation.AttachmentRef, error)
}

type CoreConfig struct {
	ChannelID        string
	CreateOptions    conversation.CreateOptions
	Locale           string
	ResponseMarkdown bool
	Localize         func(string, ...any) string
	Logger           *log.Logger
}

// ChannelCore owns authorization, pairing, conversation routing, turn execution and
// per-conversation serialization. Platform adapters own only transport and formatting.
type ChannelCore struct {
	config   CoreConfig
	runtime  Runtime
	bindings *ConversationBindingStore
	pairings *ConversationPairingStore
	provider CoreProvider

	mu      sync.Mutex
	runCtx  context.Context
	workers map[string]chan IncomingMessage
	wg      sync.WaitGroup
}

func NewChannelCore(
	config CoreConfig,
	runtime Runtime,
	bindings *ConversationBindingStore,
	pairings *ConversationPairingStore,
	provider CoreProvider,
) (*ChannelCore, error) {
	if runtime == nil {
		return nil, errors.New("channel core runtime is required")
	}
	if bindings == nil {
		return nil, errors.New("channel core bindings are required")
	}
	if pairings == nil {
		return nil, errors.New("channel core pairings are required")
	}
	if provider == nil {
		return nil, errors.New("channel core provider is required")
	}
	if strings.TrimSpace(config.ChannelID) == "" {
		return nil, errors.New("channel core channel ID is required")
	}
	if config.Localize == nil {
		return nil, errors.New("channel core localizer is required")
	}
	if config.Logger == nil {
		config.Logger = log.Default()
	}
	return &ChannelCore{
		config: config, runtime: runtime, bindings: bindings, pairings: pairings,
		provider: provider, workers: make(map[string]chan IncomingMessage),
	}, nil
}

// SetRunContext defines the lifecycle context used by asynchronous workers.
func (c *ChannelCore) SetRunContext(ctx context.Context) {
	c.mu.Lock()
	c.runCtx = ctx
	c.mu.Unlock()
}

// Enqueue accepts a platform message. Immediate commands are completed synchronously;
// other messages are serialized by external conversation.
func (c *ChannelCore) Enqueue(ctx context.Context, message IncomingMessage) (bool, error) {
	if !message.valid() {
		return false, nil
	}
	message.Content = strings.TrimSpace(message.Content)
	_, isPairing := ParsePairingCommand(message.Content)
	sessionID, err := c.bindings.Get(ctx, c.config.ChannelID, message.ConversationKey)
	if err != nil {
		return false, err
	}
	if sessionID == "" && !isPairing && !c.provider.Allowed(message) {
		return false, nil
	}
	if message.Content == "/stop" {
		if sessionID != "" {
			c.runtime.CancelTurn(sessionID)
			return false, c.send(ctx, message, "task_stopped")
		}
		return false, c.send(ctx, message, "conversation_unbound")
	}

	c.mu.Lock()
	runCtx := c.runCtx
	if runCtx == nil {
		runCtx = ctx
	}
	worker := c.workers[message.ConversationKey]
	if worker == nil {
		worker = make(chan IncomingMessage, defaultWorkerQueueSize)
		c.workers[message.ConversationKey] = worker
		c.wg.Add(1)
		go c.runWorker(runCtx, message.ConversationKey, worker)
	}
	c.mu.Unlock()

	select {
	case worker <- message:
		return true, nil
	case <-runCtx.Done():
		return false, runCtx.Err()
	default:
		return false, c.send(ctx, message, "queue_full")
	}
}

// Process executes a message synchronously. It is useful to providers with their own
// scheduling and keeps the command and turn lifecycle in one implementation.
func (c *ChannelCore) Process(ctx context.Context, message IncomingMessage) error {
	if !message.valid() {
		return nil
	}
	message.Content = strings.TrimSpace(message.Content)
	if code, ok := ParsePairingCommand(message.Content); ok {
		if code == "" {
			return c.send(ctx, message, "pairing_usage")
		}
		if _, err := c.pairings.Consume(ctx, c.config.ChannelID, code,
			message.ConversationKey, message.Kind, message.ExternalID, message.DisplayName); err != nil {
			return c.send(ctx, message, "pairing_failed")
		}
		return c.send(ctx, message, "pairing_complete")
	}
	if message.Content == "/new" {
		if err := c.observe(ctx, message); err != nil {
			return c.replyError(ctx, message, err)
		}
		if _, err := c.createSession(ctx, message.ConversationKey); err != nil {
			return c.replyError(ctx, message, err)
		}
		return c.send(ctx, message, "new_chat")
	}

	sessionID, err := c.sessionForConversation(ctx, message.ConversationKey)
	if err != nil {
		if errors.Is(err, ErrConversationUnbound) {
			return c.send(ctx, message, "conversation_unbound")
		}
		return c.replyError(ctx, message, err)
	}
	if err := c.observe(ctx, message); err != nil {
		return c.replyError(ctx, message, err)
	}
	attachments, err := c.provider.Attachments(ctx, sessionID, message)
	if err != nil {
		return c.replyError(ctx, message, err)
	}
	if message.Content == "" && len(attachments) == 0 {
		return c.send(ctx, message, "unsupported_message")
	}

	events := c.runtime.Subscribe(ctx, sessionID)
	if err := c.runtime.SubmitChatInput(ctx, sessionID, conversation.UserInput{
		Text: message.Content, Attachments: attachments,
	}); err != nil {
		return c.replyError(ctx, message, err)
	}

	var lastAssistant, finalAssistant string
	var responseErr error
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				return errors.New("Foya event stream closed before turn completion")
			}
			switch event.Kind {
			case conversation.KindMessageEnd:
				reply, ok := event.Payload.(conversation.Message)
				if !ok || reply.Role != conversation.RoleAssistant {
					continue
				}
				if reply.Content != "" {
					lastAssistant = reply.Content
				}
				if len(reply.ToolCalls) == 0 {
					finalAssistant = reply.Content
				}
			case conversation.KindApprovalReq:
				if request, ok := event.Payload.(interaction.Request); ok {
					_ = c.runtime.ResolveApproval(request.ID, string(interaction.DecisionDenied))
				}
			case conversation.KindQuestionRequested:
				if batch, ok := event.Payload.(interaction.Batch); ok {
					_ = c.runtime.CancelQuestions(sessionID, batch.ID)
				}
			case conversation.KindError:
				responseErr = fmt.Errorf("%v", event.Payload)
			case conversation.KindTurnComplete:
				response := finalAssistant
				if response == "" {
					response = lastAssistant
				}
				if responseErr != nil {
					if response != "" {
						if c.config.ResponseMarkdown {
							response += "\n\n> "
						} else {
							response += "\n\n"
						}
					}
					response += responseErr.Error()
				}
				if response == "" {
					response = c.config.Localize("empty_response")
				}
				return c.provider.Send(ctx, message, response, c.config.ResponseMarkdown)
			}
		}
	}
}

func (c *ChannelCore) Wait() { c.wg.Wait() }

func (c *ChannelCore) runWorker(ctx context.Context, key string, input <-chan IncomingMessage) {
	defer c.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-input:
			if err := c.Process(ctx, message); err != nil && !errors.Is(err, context.Canceled) {
				c.config.Logger.Printf("channel message failed: conversation=%s error=%v", key, err)
			}
		}
	}
}

func (c *ChannelCore) sessionForConversation(ctx context.Context, key string) (string, error) {
	sessionID, err := c.bindings.Get(ctx, c.config.ChannelID, key)
	if err != nil {
		return "", err
	}
	if sessionID != "" {
		for _, item := range c.runtime.ListSessions() {
			if item.ID == sessionID {
				return sessionID, nil
			}
		}
		if err := c.bindings.UnbindSession(ctx, sessionID); err != nil {
			return "", err
		}
	}
	return "", ErrConversationUnbound
}

func (c *ChannelCore) createSession(ctx context.Context, key string) (string, error) {
	created, err := c.runtime.CreateSession(c.config.CreateOptions)
	if err != nil {
		return "", err
	}
	if err := c.bindings.Bind(ctx, c.config.ChannelID, key, created.ID); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (c *ChannelCore) observe(ctx context.Context, message IncomingMessage) error {
	return c.bindings.Observe(ctx, c.config.ChannelID, message.ConversationKey,
		message.Kind, message.ExternalID, message.DisplayName)
}

func (c *ChannelCore) replyError(ctx context.Context, message IncomingMessage, err error) error {
	return errors.Join(err, c.provider.Send(ctx, message,
		c.config.Localize("request_failed", err), false))
}

func (c *ChannelCore) send(ctx context.Context, message IncomingMessage, key string) error {
	return c.provider.Send(ctx, message, c.config.Localize(key), false)
}

func (m IncomingMessage) valid() bool {
	return strings.TrimSpace(m.ConversationKey) != "" &&
		strings.TrimSpace(m.Kind) != "" &&
		strings.TrimSpace(m.ExternalID) != ""
}
