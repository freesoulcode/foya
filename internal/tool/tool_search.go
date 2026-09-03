package tool

import (
	"context"
	"encoding/json"
	"strings"
)

type toolSearchTool struct {
	registry Registry
}

type toolSearchParams struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

func NewToolSearchTool(registry Registry) Tool {
	return &toolSearchTool{registry: registry}
}

func (t *toolSearchTool) Name() string       { return "tool_search" }
func (t *toolSearchTool) Exposure() Exposure { return ExposureDirect }
func (t *toolSearchTool) Description() string {
	return "Search and activate deferred tool capabilities by task description. Activated tools become available on the next model step."
}
func (t *toolSearchTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"query":{"type":"string","description":"Short description of the needed capability, such as browser navigation or browser click."},
			"limit":{"type":"integer","minimum":1,"maximum":20,"description":"Maximum tools to activate. Defaults to 8."}
		},
		"required":["query"],
		"additionalProperties":false
	}`)
}

func (t *toolSearchTool) Run(ctx context.Context, call Call) (Result, error) {
	var params toolSearchParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: "+err.Error()), nil
	}
	params.Query = strings.TrimSpace(params.Query)
	if params.Query == "" {
		return errResult("query is required"), nil
	}
	matches := t.registry.SearchDeferred(params.Query, params.Limit)
	activator, _ := DeferredToolActivatorFromContext(ctx)
	type row struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	rows := make([]row, 0, len(matches))
	for _, item := range matches {
		if activator != nil {
			activator.ActivateTool(item.Name())
		}
		rows = append(rows, row{
			Name:        item.Name(),
			Description: item.Description(),
		})
	}
	data, _ := json.Marshal(struct {
		Activated []row `json:"activated"`
	}{
		Activated: rows,
	})
	return textResult(string(data)), nil
}
