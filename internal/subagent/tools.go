package subagent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/freesoulcode/foya/internal/tool"
)

type agentTool struct {
	manager *Manager
}

type agentParams struct {
	Task    string           `json:"task"`
	Agent   string           `json:"agent,omitempty"`
	Context ContextSelection `json:"context,omitempty"`
}

func NewTool(manager *Manager) tool.Tool {
	return &agentTool{manager: manager}
}

func (t *agentTool) Name() string            { return "agent" }
func (t *agentTool) Exposure() tool.Exposure { return tool.ExposureDirect }
func (t *agentTool) Parallel() bool          { return true }
func (t *agentTool) Description() string {
	return "Delegate an independent task to an isolated child agent. Multiple agent calls in one response run concurrently. Use agent_search first when a specialized user or project agent may apply."
}
func (t *agentTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"task":{"type":"string","description":"A self-contained task with the context and expected output"},
			"agent":{"type":"string","description":"Optional agent ref or name returned by agent_search"}
		},
		"required":["task"],
		"additionalProperties":false
	}`)
}

func (t *agentTool) Run(ctx context.Context, call tool.Call) (tool.Result, error) {
	var params agentParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errorResult("invalid arguments: " + err.Error()), nil
	}
	params.Task = strings.TrimSpace(params.Task)
	if params.Task == "" {
		return errorResult("task is required"), nil
	}
	parentID := tool.SessionIDFromContext(ctx)
	if parentID == "" {
		return errorResult("parent session is unavailable"), nil
	}
	result, err := t.manager.Spawn(ctx, SpawnRequest{
		ParentSessionID:  parentID,
		ParentToolCallID: call.ID,
		RootRunID:        tool.RunIDFromContext(ctx),
		Task:             params.Task,
		AgentRef:         strings.TrimSpace(params.Agent),
		Context:          params.Context,
	})
	data, _ := json.Marshal(result)
	if err != nil {
		return tool.Result{
			Content: []tool.ContentPart{{Type: "text", Text: string(data)}},
			IsError: true,
		}, nil
	}
	return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: string(data)}}}, nil
}

type spawnTool struct{ manager *Manager }

func NewSpawnTool(manager *Manager) tool.Tool { return &spawnTool{manager: manager} }
func (t *spawnTool) Name() string             { return "spawn_agent" }
func (t *spawnTool) Exposure() tool.Exposure  { return tool.ExposureDirect }
func (t *spawnTool) Parallel() bool           { return true }
func (t *spawnTool) Description() string {
	return "Start an isolated child agent asynchronously and return its run ID immediately."
}
func (t *spawnTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"task":{"type":"string","description":"Self-contained goal, constraints, known facts, evidence references, and expected output"},
			"agent":{"type":"string","description":"Optional agent ref returned by agent_search"},
			"context":{"type":"object","properties":{
				"mode":{"type":"string","enum":["none","selected","summary","last_n_turns"]},
				"message_seqs":{"type":"array","items":{"type":"integer"}},
				"last_turns":{"type":"integer","minimum":1,"maximum":20}
			},"additionalProperties":false}
		},
		"required":["task"],
		"additionalProperties":false
	}`)
}
func (t *spawnTool) Run(ctx context.Context, call tool.Call) (tool.Result, error) {
	var params agentParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errorResult("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(params.Task) == "" {
		return errorResult("task is required"), nil
	}
	snapshot, err := t.manager.Start(ctx, SpawnRequest{
		ParentSessionID: tool.SessionIDFromContext(ctx), ParentToolCallID: call.ID,
		RootRunID: tool.RunIDFromContext(ctx), Task: params.Task,
		AgentRef: strings.TrimSpace(params.Agent), Context: params.Context,
		CancelWithParent: true,
	})
	return jsonToolResult(snapshot, err)
}

type waitParams struct {
	IDs  []string `json:"ids"`
	Mode string   `json:"mode,omitempty"`
}
type waitTool struct{ manager *Manager }

func NewWaitTool(manager *Manager) tool.Tool { return &waitTool{manager: manager} }
func (t *waitTool) Name() string             { return "wait_agents" }
func (t *waitTool) Exposure() tool.Exposure  { return tool.ExposureDirect }
func (t *waitTool) Description() string {
	return "Wait until all or any of the specified asynchronous agent runs finish."
}
func (t *waitTool) Spec() []byte {
	return []byte(`{"type":"object","properties":{
		"ids":{"type":"array","items":{"type":"string"},"minItems":1},
		"mode":{"type":"string","enum":["all","any"]}
	},"required":["ids"],"additionalProperties":false}`)
}
func (t *waitTool) Run(ctx context.Context, call tool.Call) (tool.Result, error) {
	var params waitParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errorResult("invalid arguments: " + err.Error()), nil
	}
	items, err := t.manager.Wait(ctx, params.IDs, params.Mode != "any")
	return jsonToolResult(items, err)
}

type runIDParams struct {
	ID string `json:"id"`
}
type readTool struct{ manager *Manager }

func NewReadTool(manager *Manager) tool.Tool { return &readTool{manager: manager} }
func (t *readTool) Name() string             { return "read_agent_output" }
func (t *readTool) Exposure() tool.Exposure  { return tool.ExposureDirect }
func (t *readTool) Description() string {
	return "Read the current status and output of one asynchronous agent run."
}
func (t *readTool) Spec() []byte {
	return []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`)
}
func (t *readTool) Run(_ context.Context, call tool.Call) (tool.Result, error) {
	var params runIDParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errorResult("invalid arguments: " + err.Error()), nil
	}
	item, err := t.manager.Read(params.ID)
	return jsonToolResult(item, err)
}

type cancelTool struct{ manager *Manager }

func NewCancelTool(manager *Manager) tool.Tool { return &cancelTool{manager: manager} }
func (t *cancelTool) Name() string             { return "cancel_agent" }
func (t *cancelTool) Exposure() tool.Exposure  { return tool.ExposureDirect }
func (t *cancelTool) Description() string      { return "Cancel one queued or running agent run." }
func (t *cancelTool) Spec() []byte {
	return []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`)
}
func (t *cancelTool) Run(_ context.Context, call tool.Call) (tool.Result, error) {
	var params runIDParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errorResult("invalid arguments: " + err.Error()), nil
	}
	err := t.manager.Cancel(params.ID)
	item, readErr := t.manager.Read(params.ID)
	if err == nil {
		err = readErr
	}
	return jsonToolResult(item, err)
}

type listTool struct{ manager *Manager }

func NewListTool(manager *Manager) tool.Tool { return &listTool{manager: manager} }
func (t *listTool) Name() string             { return "list_agents" }
func (t *listTool) Exposure() tool.Exposure  { return tool.ExposureDirect }
func (t *listTool) Description() string {
	return "List asynchronous child agent runs started by the current session."
}
func (t *listTool) Spec() []byte {
	return []byte(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (t *listTool) Run(ctx context.Context, _ tool.Call) (tool.Result, error) {
	return jsonToolResult(t.manager.List(tool.SessionIDFromContext(ctx)), nil)
}

func jsonToolResult(value any, err error) (tool.Result, error) {
	if err != nil {
		return errorResult(err.Error()), nil
	}
	data, _ := json.Marshal(value)
	return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: string(data)}}}, nil
}

type searchTool struct {
	definitions    *DefinitionManager
	resolveProject ProjectResolver
}

type searchParams struct {
	Query string `json:"query,omitempty"`
}

func NewSearchTool(definitions *DefinitionManager, resolveProject ProjectResolver) tool.Tool {
	return &searchTool{definitions: definitions, resolveProject: resolveProject}
}

func (t *searchTool) Name() string            { return "agent_search" }
func (t *searchTool) Exposure() tool.Exposure { return tool.ExposureDirect }
func (t *searchTool) Description() string {
	return "Search available builtin, user, and project agent definitions by name and description."
}
func (t *searchTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{"query":{"type":"string","description":"Optional agent name or capability query"}},
		"additionalProperties":false
	}`)
}

func (t *searchTool) Run(ctx context.Context, call tool.Call) (tool.Result, error) {
	var params searchParams
	if len(call.Input) > 0 {
		if err := json.Unmarshal(call.Input, &params); err != nil {
			return errorResult("invalid arguments: " + err.Error()), nil
		}
	}
	projectID := tool.ProjectIDFromContext(ctx)
	projectPath := ""
	if projectID != "" && t.resolveProject != nil {
		projectPath, _ = t.resolveProject(projectID)
	}
	items, err := t.definitions.List(ctx, projectID, projectPath)
	if err != nil {
		return errorResult("agent discovery failed: " + err.Error()), nil
	}
	query := strings.ToLower(strings.TrimSpace(params.Query))
	type row struct {
		Ref         string   `json:"ref"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Scope       Scope    `json:"scope"`
		Model       string   `json:"model,omitempty"`
		Tools       []string `json:"tools,omitempty"`
	}
	rows := make([]row, 0, len(items))
	for _, item := range items {
		haystack := strings.ToLower(item.Name + "\n" + item.Description)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		rows = append(rows, row{
			Ref: item.Ref, Name: item.Name, Description: item.Description,
			Scope: item.Scope, Model: item.Model, Tools: item.Tools,
		})
	}
	data, _ := json.Marshal(rows)
	return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: string(data)}}}, nil
}

func errorResult(text string) tool.Result {
	return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: text}}, IsError: true}
}
