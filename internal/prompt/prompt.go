package prompt

import (
	"os"
	"runtime"
	"strings"
	"time"
)

// Input 是组装一次系统提示词所需的全部输入。
//
// 零值字段会被合理兜底(平台取 runtime.GOOS、shell 取环境变量、
// 时间取 now、home 取 os.UserHomeDir),调用方只需关心 project path
// 和 approvalMode 等会话级信息。
type Input struct {
	ProjectPath  string    // 当前项目目录
	ApprovalMode string    // 审批档位:manual / auto / full_access
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
	platform, shell := effectiveEnvironment(in, runtime.GOOS)

	fragments := []string{
		staticPrefix,
		loadProjectInstructions(home, in.ProjectPath),
		permissionFragment(in.ApprovalMode),
		envFragment(EnvInput{
			Cwd:      in.ProjectPath,
			Platform: platform,
			Shell:    shell,
			Now:      now,
		}),
	}

	return joinFragments(fragments)
}

func effectiveEnvironment(in Input, goos string) (platform, shell string) {
	platform = in.Platform
	shell = in.Shell
	if goos == "windows" && in.ApprovalMode != "full_access" {
		if platform == "" {
			platform = "linux (WSL2 sandbox on Windows host)"
		}
		if shell == "" {
			shell = "/bin/sh"
		}
	}
	return platform, shell
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
