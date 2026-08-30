package mcpclient

import (
	"context"
	"encoding/json"
	"strings"

	foyatool "github.com/freesoulcode/foya/internal/tool"
)

type controlTool struct {
	manager     *Manager
	name        string
	description string
	schema      []byte
	run         func(context.Context, map[string]any) (any, error)
}

func (t *controlTool) Name() string                { return t.name }
func (t *controlTool) Description() string         { return t.description }
func (t *controlTool) Spec() []byte                { return t.schema }
func (t *controlTool) Exposure() foyatool.Exposure { return foyatool.ExposureDirect }
func (t *controlTool) Run(ctx context.Context, call foyatool.Call) (foyatool.Result, error) {
	var input map[string]any
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return mcpError("invalid arguments: " + err.Error()), nil
	}
	value, err := t.run(ctx, input)
	if err != nil {
		return mcpError(err.Error()), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return mcpError(err.Error()), nil
	}
	return foyatool.Result{Content: []foyatool.ContentPart{{Type: "text", Text: string(data)}}}, nil
}

func ControlTools(manager *Manager) []foyatool.Tool {
	serverSchema := `"server_id":{"type":"string","description":"Configured MCP server id"}`
	return []foyatool.Tool{
		&controlTool{
			manager: manager, name: "mcp_list_resources",
			description: "List resources exposed by a connected MCP server.",
			schema:      []byte(`{"type":"object","properties":{` + serverSchema + `},"required":["server_id"],"additionalProperties":false}`),
			run: func(ctx context.Context, input map[string]any) (any, error) {
				return manager.Resources(ctx, stringArg(input, "server_id"))
			},
		},
		&controlTool{
			manager: manager, name: "mcp_read_resource",
			description: "Read one resource from a connected MCP server.",
			schema: []byte(`{"type":"object","properties":{` + serverSchema +
				`,"uri":{"type":"string"}},"required":["server_id","uri"],"additionalProperties":false}`),
			run: func(ctx context.Context, input map[string]any) (any, error) {
				return manager.ReadResource(ctx, stringArg(input, "server_id"), stringArg(input, "uri"))
			},
		},
		&controlTool{
			manager: manager, name: "mcp_list_prompts",
			description: "List prompts exposed by a connected MCP server.",
			schema:      []byte(`{"type":"object","properties":{` + serverSchema + `},"required":["server_id"],"additionalProperties":false}`),
			run: func(ctx context.Context, input map[string]any) (any, error) {
				return manager.Prompts(ctx, stringArg(input, "server_id"))
			},
		},
		&controlTool{
			manager: manager, name: "mcp_get_prompt",
			description: "Render one prompt exposed by a connected MCP server.",
			schema: []byte(`{"type":"object","properties":{` + serverSchema +
				`,"name":{"type":"string"},"args":{"type":"object","additionalProperties":{"type":"string"}}},` +
				`"required":["server_id","name"],"additionalProperties":false}`),
			run: func(ctx context.Context, input map[string]any) (any, error) {
				args := make(map[string]string)
				if raw, ok := input["args"].(map[string]any); ok {
					for key, value := range raw {
						if text, ok := value.(string); ok {
							args[key] = text
						}
					}
				}
				return manager.GetPrompt(ctx, stringArg(input, "server_id"), stringArg(input, "name"), args)
			},
		},
	}
}

func stringArg(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}
