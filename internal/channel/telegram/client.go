package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const telegramAPIBaseURL = "https://api.telegram.org"

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type Message struct {
	ID      int64  `json:"message_id"`
	Chat    Chat   `json:"chat"`
	From    *User  `json:"from"`
	Text    string `json:"text"`
	Caption string `json:"caption"`
}

type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message"`
}

type API interface {
	GetMe(context.Context) (User, error)
	GetUpdates(context.Context, int64) ([]Update, error)
	SendMessage(context.Context, int64, string) error
}

type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		token:   strings.TrimSpace(token),
		baseURL: telegramAPIBaseURL,
		httpClient: &http.Client{
			Timeout: 40 * time.Second,
		},
	}
}

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var result User
	if err := c.call(ctx, "getMe", nil, &result); err != nil {
		return User{}, err
	}
	return result, nil
}

func (c *Client) GetUpdates(ctx context.Context, offset int64) ([]Update, error) {
	values := url.Values{
		"timeout":         {"30"},
		"allowed_updates": {`["message"]`},
	}
	if offset > 0 {
		values.Set("offset", strconv.FormatInt(offset, 10))
	}
	var result []Update
	if err := c.call(ctx, "getUpdates", values, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	values := url.Values{
		"chat_id": {strconv.FormatInt(chatID, 10)},
		"text":    {text},
	}
	return c.call(ctx, "sendMessage", values, nil)
}

func (c *Client) call(
	ctx context.Context,
	method string,
	values url.Values,
	result any,
) error {
	if c == nil || c.token == "" {
		return errors.New("Telegram bot token is required")
	}
	if values == nil {
		values = url.Values{}
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/bot%s/%s", strings.TrimRight(c.baseURL, "/"), c.token, method),
		bytes.NewBufferString(values.Encode()),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode Telegram %s response: %w", method, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.OK {
		message := strings.TrimSpace(envelope.Description)
		if message == "" {
			message = response.Status
		}
		return fmt.Errorf("Telegram %s failed: %s", method, message)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("decode Telegram %s result: %w", method, err)
	}
	return nil
}
