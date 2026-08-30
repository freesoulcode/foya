package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/freesoulcode/foya/internal/approval"
	foyatool "github.com/freesoulcode/foya/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var unsafeToolName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

type remoteTool struct {
	manager     *Manager
	serverID    string
	remoteName  string
	name        string
	description string
	schema      []byte
}

func newMCPTool(manager *Manager, serverID string, remote *mcp.Tool) foyatool.Tool {
	schema, _ := json.Marshal(remote.InputSchema)
	if len(schema) == 0 || string(schema) == "null" {
		schema = []byte(`{"type":"object","properties":{}}`)
	}
	return &remoteTool{
		manager: manager, serverID: serverID, remoteName: remote.Name,
		name: mcpToolName(serverID, remote.Name), description: remote.Description, schema: schema,
	}
}

func (t *remoteTool) Name() string                { return t.name }
func (t *remoteTool) Description() string         { return t.description }
func (t *remoteTool) Spec() []byte                { return t.schema }
func (t *remoteTool) Exposure() foyatool.Exposure { return foyatool.ExposureDirect }

func (t *remoteTool) Run(ctx context.Context, call foyatool.Call) (foyatool.Result, error) {
	var arguments map[string]any
	if len(call.Input) > 0 {
		if err := json.Unmarshal(call.Input, &arguments); err != nil {
			return mcpError("invalid MCP tool arguments: " + err.Error()), nil
		}
	}
	decision, err := t.manager.gateway.Request(ctx, approval.Request{
		ToolName: t.name, Action: "execute",
		Detail: fmt.Sprintf("调用 MCP 工具 %s/%s", t.serverID, t.remoteName),
	})
	if err != nil {
		return mcpError("MCP approval interrupted: " + err.Error()), nil
	}
	if decision == approval.DecisionDenied {
		return mcpError("user denied MCP tool execution"), nil
	}
	result, err := t.manager.Call(ctx, t.serverID, t.remoteName, arguments)
	if err != nil {
		return mcpError("MCP tool failed: " + err.Error()), nil
	}
	parts := make([]foyatool.ContentPart, 0, len(result.Content)+1)
	for _, content := range result.Content {
		switch value := content.(type) {
		case *mcp.TextContent:
			parts = append(parts, foyatool.ContentPart{Type: "text", Text: value.Text})
		case *mcp.ImageContent:
			parts = append(parts, foyatool.ContentPart{
				Type: "text",
				Text: fmt.Sprintf("[MCP image omitted from model context: %s, %d bytes]", value.MIMEType, len(value.Data)),
			})
		case *mcp.AudioContent:
			parts = append(parts, foyatool.ContentPart{
				Type: "text",
				Text: fmt.Sprintf("[MCP audio omitted from model context: %s, %d bytes]", value.MIMEType, len(value.Data)),
			})
		default:
			data, marshalErr := content.MarshalJSON()
			if marshalErr == nil {
				parts = append(parts, foyatool.ContentPart{Type: "text", Text: string(data)})
			}
		}
	}
	if result.StructuredContent != nil {
		if data, marshalErr := json.Marshal(result.StructuredContent); marshalErr == nil {
			parts = append(parts, foyatool.ContentPart{Type: "text", Text: string(data)})
		}
	}
	if len(parts) == 0 {
		parts = append(parts, foyatool.ContentPart{Type: "text", Text: "(no output)"})
	}
	return foyatool.Result{Content: parts, IsError: result.IsError}, nil
}

func mcpToolName(serverID, toolName string) string {
	serverID = strings.Trim(unsafeToolName.ReplaceAllString(serverID, "_"), "_")
	toolName = strings.Trim(unsafeToolName.ReplaceAllString(toolName, "_"), "_")
	name := "mcp__" + serverID + "__" + toolName
	if len(name) > 64 {
		sum := sha256.Sum256([]byte(name))
		name = name[:51] + "__" + fmt.Sprintf("%x", sum[:5])
	}
	return name
}

func mcpError(message string) foyatool.Result {
	return foyatool.Result{
		IsError: true,
		Content: []foyatool.ContentPart{{Type: "text", Text: message}},
	}
}
