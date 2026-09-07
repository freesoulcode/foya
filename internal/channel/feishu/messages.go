package feishu

import "fmt"

type messageCatalog map[string]string

var messageCatalogs = map[string]messageCatalog{
	"en-US": {
		"task_stopped":           "Stopped the current task.",
		"no_task_to_stop":        "There is no active task to stop.",
		"queue_full":             "The message queue is full. Try again later.",
		"new_chat":               "Started a new chat.",
		"unsupported_message":    "Only text and image messages are currently supported.",
		"empty_response":         "The task finished without a text response.",
		"unsupported_attachment": "Attachments of type %s are not supported.",
		"download_image_failed":  "Failed to download image: %v",
		"import_image_failed":    "Failed to import image: %v",
		"request_failed":         "Request failed: %v",
		"app_description":        "A Feishu assistant powered by the Foya desktop app",
		"missing_credentials":    "Feishu did not return application credentials",
		"unsupported_account":    "Only Feishu accounts are currently supported",
	},
	"zh-CN": {
		"task_stopped":           "已停止当前任务。",
		"no_task_to_stop":        "当前没有可停止的任务。",
		"queue_full":             "消息队列已满，请稍后重试。",
		"new_chat":               "已开启新会话。",
		"unsupported_message":    "暂时只支持文本和图片消息。",
		"empty_response":         "任务已结束，但没有生成文本回复。",
		"unsupported_attachment": "暂时不支持 %s 类型的附件。",
		"download_image_failed":  "下载图片失败：%v",
		"import_image_failed":    "导入图片失败：%v",
		"request_failed":         "请求失败：%v",
		"app_description":        "通过 Foya 桌面端运行的飞书智能助手",
		"missing_credentials":    "飞书未返回应用凭证",
		"unsupported_account":    "当前仅支持飞书账号",
	},
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
