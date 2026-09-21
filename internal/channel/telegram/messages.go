package telegram

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

type messageCatalog map[string]string

//go:embed messages.zh-CN.json
var zhMessageCatalogJSON []byte

var messageCatalogs = map[string]messageCatalog{
	"en-US": {
		"task_stopped":         "Stopped the current task.",
		"no_task_to_stop":      "There is no active task to stop.",
		"queue_full":           "The message queue is full. Try again later.",
		"new_chat":             "Started a new chat.",
		"conversation_unbound": "This chat is not connected. Use /new to start a Foya chat, or send the /bind code shown in Foya.",
		"pairing_usage":        "Use /bind CODE to connect this chat.",
		"pairing_failed":       "That pairing code is invalid or expired.",
		"pairing_complete":     "This chat is now connected to Foya.",
		"unsupported_message":  "Only text messages are currently supported.",
		"empty_response":       "The task finished without a text response.",
		"request_failed":       "Request failed: %v",
	},
	"zh-CN": mustMessageCatalog(zhMessageCatalogJSON),
}

func mustMessageCatalog(data []byte) messageCatalog {
	var catalog messageCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		panic(fmt.Errorf("decode embedded message catalog: %w", err))
	}
	return catalog
}

func localizedMessage(locale, key string, args ...any) string {
	catalog := messageCatalogs[locale]
	if catalog == nil {
		catalog = messageCatalogs["en-US"]
	}
	template := catalog[key]
	if template == "" {
		template = messageCatalogs["en-US"][key]
	}
	if len(args) == 0 {
		return template
	}
	return fmt.Sprintf(template, args...)
}
