package prompt

import (
	"os"
	"strings"
	"time"
)

// Input 是组装一次系统提示词所需的全部输入。
//
// 零值字段会被合理兜底(平台取 runtime.GOOS、shell 取环境变量、
// 时间取 now、home 取 os.UserHomeDir),调用方只需关心 workspace
// 和 approvalMode 等会话级信息。
type Input struct {
	Workspace    string    // 当前工作目录
	ApprovalMode string    // 审批档位:explore / ask / bypass
	Platform     string    // 留空则自动推断
	Shell        string    // 留空则自动推断
	Now          time.Time // 留空则取 time.Now()
	HomeDir      string    // 留空则取 os.UserHomeDir()
}

// Assemble 组装最终系统提示词:静态前缀 → 工作区指令 → 权限上下文 → 环境尾部。
//
// 静态前缀字节稳定以命中前缀缓存;工作区指令为用户可控不可信内容(已降权包裹);
// 权限/环境为每回合实时数据,置于末尾。组装结果不写入事件日志,仅用于本次模型请求。
func Assemble(in Input) string {
	home := in.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}

	fragments := []string{
		staticPrefix,
		loadWorkspaceInstructions(home, in.Workspace),
		permissionFragment(in.ApprovalMode),
		envFragment(EnvInput{
			Cwd:      in.Workspace,
			Platform: in.Platform,
			Shell:    in.Shell,
			Now:      now,
		}),
	}

	return joinFragments(fragments)
}

// joinFragments 跳过空片段,用空行连接。
func joinFragments(parts []string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n")
}
