package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientCallsTelegramBotAPI(t *testing.T) {
	var sentChatID string
	var sentText string
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/botsecret/getMe":
			_, _ = w.Write([]byte(
				`{"ok":true,"result":{"id":1,"is_bot":true,"username":"foya_bot"}}`,
			))
		case "/botsecret/getUpdates":
			if got := r.Form.Get("offset"); got != "12" {
				t.Errorf("offset = %q, want 12", got)
			}
			_, _ = w.Write([]byte(
				`{"ok":true,"result":[{"update_id":12,"message":{"message_id":3,"chat":{"id":101,"type":"private"},"text":"hello"}}]}`,
			))
		case "/botsecret/sendMessage":
			sentChatID = r.Form.Get("chat_id")
			sentText = r.Form.Get("text")
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient("secret")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	me, err := client.GetMe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if me.Username != "foya_bot" {
		t.Fatalf("bot = %#v", me)
	}
	updates, err := client.GetUpdates(context.Background(), 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].Message == nil ||
		updates[0].Message.Chat.ID != 101 {
		t.Fatalf("updates = %#v", updates)
	}
	if err := client.SendMessage(context.Background(), 101, "reply"); err != nil {
		t.Fatal(err)
	}
	if sentChatID != "101" || sentText != "reply" {
		t.Fatalf("send payload = chat_id %q, text %q", sentChatID, sentText)
	}
}

func TestClientReturnsTelegramError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	}))
	defer server.Close()

	client := NewClient("invalid")
	client.baseURL = server.URL
	client.httpClient = server.Client()
	if _, err := client.GetMe(context.Background()); err == nil {
		t.Fatal("GetMe succeeded for a rejected token")
	}
}
