package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/freesoulcode/foya/internal/contextdata"
)

type ruleReader interface {
	GetRule(projectID, ref string) (contextdata.Rule, error)
}

type ruleLoadTool struct {
	store ruleReader
}

type ruleLoadParams struct {
	Name string `json:"name"`
}

func NewRuleLoadTool(store ruleReader) Tool {
	return &ruleLoadTool{store: store}
}

func (t *ruleLoadTool) Name() string       { return "rule_load" }
func (t *ruleLoadTool) Exposure() Exposure { return ExposureDirect }
func (t *ruleLoadTool) Description() string {
	return "Load one relevant Foya rule whose name and description are listed in the available-rules prompt section."
}
func (t *ruleLoadTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{"name":{"type":"string","description":"Exact rule name or id"}},
		"required":["name"],
		"additionalProperties":false
	}`)
}

func (t *ruleLoadTool) Run(ctx context.Context, call Call) (Result, error) {
	var params ruleLoadParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return errResult("rule name is required"), nil
	}
	item, err := t.store.GetRule(ProjectIDFromContext(ctx), name)
	if err != nil {
		return errResult("rule load failed: " + err.Error()), nil
	}
	return textResult(fmt.Sprintf(
		"<foya_rule name=%q scope=%q>\n%s\n</foya_rule>",
		item.Name,
		item.Scope,
		item.Content,
	)), nil
}
