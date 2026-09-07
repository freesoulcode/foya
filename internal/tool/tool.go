// Package tool defines tool interfaces, registration, and routing.
//
// Tools expose schemas to the model, route calls to handlers, execute actions,
// and return structured results. They are the kernel's core extension point.
package tool

import (
	"context"

	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
)

// Exposure controls tool visibility to the model.
type Exposure string

const (
	ExposureDirect   Exposure = "direct"   // Included in the initial tool list.
	ExposureDeferred Exposure = "deferred" // Loaded through tool-search when needed.
	ExposureHidden   Exposure = "hidden"   // Never exposed to the model.
)

// Call is a tool invocation requested by the model.
type Call struct {
	ID    string
	Name  string
	Input []byte // Raw JSON arguments.
}

// ContentPart is one text, image, or artifact result block.
type ContentPart struct {
	Type       string // text / image / artifact_ref
	Text       string
	Name       string
	MediaType  string
	Data       []byte
	Attachment *message.AttachmentRef
}

// Result is the outcome of a tool execution.
type Result struct {
	Content    []ContentPart
	IsError    bool // Errors are returned to the model for self-correction.
	Terminate  bool // Whether to end the current turn batch early.
	FileChange *message.FileChange
	// Diff is a unified file diff for UI rendering and is not sent to the model.
	Diff string
}

// Tool is an executable operation available to the model.
type Tool interface {
	Name() string
	Description() string // Description shown to the model.
	Spec() []byte        // JSON Schema sent to the model.
	Exposure() Exposure
	Run(ctx context.Context, call Call) (Result, error)
}

// ParallelTool marks tools that are safe to execute concurrently with other
// parallel tool calls from the same model response.
type ParallelTool interface {
	Tool
	Parallel() bool
}

// Registry manages available tools.
type Registry interface {
	Register(t Tool)         // Built-in tool.
	RegisterExternal(t Tool) // MCP or dynamic tool, deduplicated by name.
	Unregister(name string)
	Get(name string) (Tool, bool)
	List() []Tool
	Specs() []provider.ToolDef // Tool definitions sent to the model.
	SpecsFor(activeDeferred map[string]bool) []provider.ToolDef
	SearchDeferred(query string, limit int) []Tool
}
