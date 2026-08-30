package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/freesoulcode/foya/internal/skill"
)

type skillSearchTool struct {
	manager *skill.Manager
}

type skillLoadTool struct {
	manager *skill.Manager
}

type skillSearchParams struct {
	Query string `json:"query,omitempty"`
}

type skillLoadParams struct {
	Name string `json:"name"`
}

func NewSkillSearchTool(manager *skill.Manager) Tool {
	return &skillSearchTool{manager: manager}
}

func NewSkillLoadTool(manager *skill.Manager) Tool {
	return &skillLoadTool{manager: manager}
}

func (t *skillSearchTool) Name() string       { return "skill_search" }
func (t *skillSearchTool) Exposure() Exposure { return ExposureDirect }
func (t *skillSearchTool) Description() string {
	return "Search locally installed agent skills by name and description."
}
func (t *skillSearchTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{"query":{"type":"string","description":"Optional skill name or capability query"}},
		"additionalProperties":false
	}`)
}

func (t *skillSearchTool) Run(ctx context.Context, call Call) (Result, error) {
	var params skillSearchParams
	if len(call.Input) > 0 {
		if err := json.Unmarshal(call.Input, &params); err != nil {
			return errResult("invalid arguments: " + err.Error()), nil
		}
	}
	items, err := t.manager.List(
		ctx,
		ProjectIDFromContext(ctx),
		CWDFromContext(ctx),
	)
	if err != nil {
		return errResult("skill discovery failed: " + err.Error()), nil
	}
	query := strings.ToLower(strings.TrimSpace(params.Query))
	type row struct {
		Ref          string   `json:"ref"`
		Name         string   `json:"name"`
		Description  string   `json:"description,omitempty"`
		Scope        string   `json:"scope"`
		AllowedTools []string `json:"allowed_tools,omitempty"`
	}
	rows := make([]row, 0, len(items))
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		haystack := strings.ToLower(item.Name + "\n" + item.Description)
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		rows = append(rows, row{
			Ref:          item.Ref,
			Name:         item.Name,
			Description:  item.Description,
			Scope:        string(item.Scope),
			AllowedTools: item.AllowedTools,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	data, _ := json.Marshal(rows)
	return textResult(string(data)), nil
}

func (t *skillLoadTool) Name() string       { return "skill_load" }
func (t *skillLoadTool) Exposure() Exposure { return ExposureDirect }
func (t *skillLoadTool) Description() string {
	return "Load the instructions for one locally installed agent skill."
}
func (t *skillLoadTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{"name":{"type":"string","description":"Exact skill ref or name returned by skill_search"}},
		"required":["name"],
		"additionalProperties":false
	}`)
}

func (t *skillLoadTool) Run(ctx context.Context, call Call) (Result, error) {
	var params skillLoadParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	if strings.TrimSpace(params.Name) == "" {
		return errResult("skill name is required"), nil
	}
	item, err := t.manager.Get(
		ctx,
		ProjectIDFromContext(ctx),
		CWDFromContext(ctx),
		params.Name,
	)
	if err != nil {
		return errResult("skill load failed: " + err.Error()), nil
	}
	content := fmt.Sprintf(
		"<skill name=%q ref=%q scope=%q>\n%s\n</skill>",
		item.Name,
		item.Ref,
		item.Scope,
		item.Body,
	)
	return textResult(content), nil
}

func textResult(text string) Result {
	return Result{Content: []ContentPart{{Type: "text", Text: text}}}
}
