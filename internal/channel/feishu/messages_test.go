package feishu

import "testing"

func TestLocalizedMessage(t *testing.T) {
	tests := []struct {
		name   string
		locale string
		key    string
		args   []any
		want   string
	}{
		{
			name:   "english",
			locale: "en-US",
			key:    "task_stopped",
			want:   "Stopped the current task.",
		},
		{
			name:   "chinese",
			locale: "zh-CN",
			key:    "task_stopped",
			want:   "已停止当前任务。",
		},
		{
			name:   "format arguments",
			locale: "zh-CN",
			key:    "unsupported_attachment",
			args:   []any{"file"},
			want:   "暂时不支持 file 类型的附件。",
		},
		{
			name:   "fallback locale",
			locale: "unknown",
			key:    "new_chat",
			want:   "Started a new chat.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := localizedMessage(test.locale, test.key, test.args...); got != test.want {
				t.Fatalf("localizedMessage() = %q, want %q", got, test.want)
			}
		})
	}
}
