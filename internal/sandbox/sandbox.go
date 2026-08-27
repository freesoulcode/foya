// Package sandbox 约束工具执行的副作用爆炸半径。
//
// 把原始命令改写成「隔离包装后的命令」,跨平台。本地桌面场景 MVP 可先
// 用 None + 审批兜底;云端自部署时必须启用(Linux landlock/seccomp)。
// 沙箱是能力的约束层,不是能力本身。
package sandbox

// Kind 标识沙箱后端。
type Kind string

const (
	KindNone              Kind = "none"
	KindMacSeatbelt       Kind = "mac_seatbelt"
	KindLinuxLandlock     Kind = "linux_landlock_seccomp"
	KindWindowsRestricted Kind = "windows_restricted_token"
)

// FSAccess 是文件系统访问级别。
type FSAccess string

const (
	FSReadOnly       FSAccess = "read"
	FSWorkspaceWrite FSAccess = "workspace_write"
	FSFull           FSAccess = "full"
)

// Profile 描述一次执行的隔离约束。
type Profile struct {
	FileSystem    FSAccess
	WritableRoots []string
	Network       bool
}

// ExecRequest 是待执行的命令。
type ExecRequest struct {
	Argv []string
	Dir  string
	Env  []string
}

// Sandbox 把原始命令改写成沙箱包装后的命令。
type Sandbox interface {
	Wrap(req ExecRequest, profile Profile) (ExecRequest, error)
	Kind() Kind
}
