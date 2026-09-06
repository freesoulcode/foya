package workflow

import (
	"context"
	"encoding/json"

	"github.com/freesoulcode/foya/internal/tool"
)

const SubmitSpecToolName = "workflow_submit_spec"

type submitSpecTool struct {
	manager *Manager
}

func NewSubmitSpecTool(manager *Manager) tool.Tool {
	return &submitSpecTool{manager: manager}
}

func (t *submitSpecTool) Name() string            { return SubmitSpecToolName }
func (t *submitSpecTool) Exposure() tool.Exposure { return tool.ExposureHidden }
func (t *submitSpecTool) Description() string {
	return "Submit the complete spec.md, tasks.md, and checklist.md documents for the active Spec workflow."
}

func (t *submitSpecTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"title":{"type":"string","description":"Concise task title, 2-6 words, used for the Spec folder name."},
			"spec":{"type":"string","description":"Complete Markdown content for spec.md."},
			"tasks":{"type":"string","description":"Complete Markdown task list for tasks.md. Include Markdown checkboxes."},
			"checklist":{"type":"string","description":"Complete Markdown acceptance checklist for checklist.md. Include Markdown checkboxes."}
		},
		"required":["title","spec","tasks","checklist"],
		"additionalProperties":false
	}`)
}

func (t *submitSpecTool) Run(ctx context.Context, call tool.Call) (tool.Result, error) {
	if t.manager == nil {
		return specToolError("workflow storage is unavailable"), nil
	}
	sessionID := tool.SessionIDFromContext(ctx)
	if sessionID == "" {
		return specToolError("session id is required"), nil
	}
	var documents SpecDocuments
	if err := json.Unmarshal(call.Input, &documents); err != nil {
		return specToolError("invalid arguments: " + err.Error()), nil
	}
	record, err := t.manager.SubmitSpec(sessionID, documents)
	if err != nil {
		return specToolError(err.Error()), nil
	}
	data, _ := json.Marshal(struct {
		Status    Status        `json:"status"`
		Artifacts SpecArtifacts `json:"artifacts"`
	}{
		Status:    record.Status,
		Artifacts: *record.Artifacts,
	})
	return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: string(data)}}}, nil
}

func specToolError(message string) tool.Result {
	return tool.Result{
		IsError: true,
		Content: []tool.ContentPart{{Type: "text", Text: message}},
	}
}
