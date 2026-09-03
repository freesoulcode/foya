// Package tool 定义工具接口、注册表与路由。
//
// 工具是模型可调用的执行单元:声明喂给模型的 schema、把模型返回的
// 调用路由到 handler、执行并结构化回灌。工具是内核的核心扩展点——
// 子 agent、Computer Use 等能力都以工具形态暴露给模型。
package tool

import (
	"context"

	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
)

// Exposure 控制工具对模型的可见性。
type Exposure string

const (
	ExposureDirect   Exposure = "direct"   // 初始即在模型工具列表中
	ExposureDeferred Exposure = "deferred" // 延迟加载 schema,经 tool-search 拉取(省 token)
	ExposureHidden   Exposure = "hidden"   // 不暴露给模型
)

// Call 是模型发起的一次工具调用。
type Call struct {
	ID    string
	Name  string
	Input []byte // 原始 JSON 参数
}

// ContentPart 是工具结果的一个内容块(文本 / 图片 / artifact 引用)。
type ContentPart struct {
	Type       string // text / image / artifact_ref
	Text       string
	Name       string
	MediaType  string
	Data       []byte
	Attachment *message.AttachmentRef
}

// Result 是工具执行结果。
type Result struct {
	Content   []ContentPart
	IsError   bool // 失败也结构化回灌给模型自我修正
	Terminate bool // 是否提前结束该回合批次
	// Diff 是文件变更的统一 diff 文本(仅 write/edit 等文件工具填充)。
	// 仅供 UI 展示,不回灌模型(与 message.Reasoning 同样处理),避免浪费 token。
	Diff string
}

// Tool 是模型可调用的执行单元。
type Tool interface {
	Name() string
	Description() string // 给模型看的工具说明
	Spec() []byte        // 参数 JSON Schema,喂给模型
	Exposure() Exposure
	Run(ctx context.Context, call Call) (Result, error)
}

// ParallelTool marks tools that are safe to execute concurrently with other
// parallel tool calls from the same model response.
type ParallelTool interface {
	Tool
	Parallel() bool
}

// Registry 管理工具集合。
type Registry interface {
	Register(t Tool)         // 内置工具
	RegisterExternal(t Tool) // MCP / 动态工具,可去重
	Unregister(name string)
	Get(name string) (Tool, bool)
	List() []Tool
	Specs() []provider.ToolDef                    // 生成喂给模型的工具定义
	SpecsFor(activeDeferred map[string]bool) []provider.ToolDef
	SearchDeferred(query string, limit int) []Tool
}
