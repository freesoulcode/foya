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
	manager        *skill.Manager
	availableTools map[string]bool
	capabilities   map[string]bool
}

type skillLoadTool struct {
	manager        *skill.Manager
	availableTools map[string]bool
	capabilities   map[string]bool
}

type skillReadResourceTool struct {
	manager        *skill.Manager
	availableTools map[string]bool
	capabilities   map[string]bool
}

type skillSearchParams struct {
	Query string `json:"query,omitempty"`
}

type skillLoadParams struct {
	Name string `json:"name"`
}

type skillReadResourceParams struct {
	SkillRef string `json:"skill_ref"`
	Path     string `json:"path"`
}

func NewSkillSearchTool(manager *skill.Manager, availableTools map[string]bool, capabilities map[string]bool) Tool {
	return &skillSearchTool{manager: manager, availableTools: availableTools, capabilities: capabilities}
}

func NewSkillLoadTool(manager *skill.Manager, availableTools map[string]bool, capabilities map[string]bool) Tool {
	return &skillLoadTool{manager: manager, availableTools: availableTools, capabilities: capabilities}
}

func NewSkillReadResourceTool(manager *skill.Manager, availableTools map[string]bool, capabilities map[string]bool) Tool {
	return &skillReadResourceTool{manager: manager, availableTools: availableTools, capabilities: capabilities}
}

func DefaultSkillAvailableTools() map[string]bool {
	return boolSet(
		"ask_user",
		"bash", "bash_status", "bash_cancel",
		"browser_navigate", "browser_snapshot", "browser_click",
		"browser_type", "browser_press_key", "browser_wait",
		"browser_scroll", "browser_extract", "browser_screenshot",
		"edit", "memory_remember", "read", "rule_load",
		"skill_search", "skill_load", "skill_read_resource",
		"tool_search", "web_search", "web_fetch", "write",
	)
}

func DefaultSkillCapabilities() map[string]bool {
	return boolSet(
		"browser", "filesystem", "terminal", "network", "web_search",
		"skill_resources", "memory", "rules", "approval",
	)
}

func boolSet(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
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
		Ref                  string           `json:"ref"`
		Name                 string           `json:"name"`
		Description          string           `json:"description,omitempty"`
		Scope                string           `json:"scope"`
		Pinned               bool             `json:"pinned,omitempty"`
		AllowedTools         []string         `json:"allowed_tools,omitempty"`
		RequiredTools        []string         `json:"required_tools,omitempty"`
		RequiredCapabilities []string         `json:"required_capabilities,omitempty"`
		Resources            []skill.Resource `json:"resources,omitempty"`
		Score                int              `json:"score,omitempty"`
	}
	rows := make([]row, 0, len(items))
	for _, item := range items {
		if !item.Enabled || !skill.IsInvocable(item, t.availableTools, t.capabilities) {
			continue
		}
		score := skillSearchScore(item, query)
		if query != "" && score == 0 {
			continue
		}
		rows = append(rows, row{
			Ref:                  item.Ref,
			Name:                 item.Name,
			Description:          item.Description,
			Scope:                string(item.Scope),
			Pinned:               item.Pinned,
			AllowedTools:         item.AllowedTools,
			RequiredTools:        item.RequiredTools,
			RequiredCapabilities: item.RequiredCapabilities,
			Resources:            item.Resources,
			Score:                score,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score
		}
		return rows[i].Name < rows[j].Name
	})
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
	if missingTools, missingCaps := skill.MissingRequirements(item, t.availableTools, t.capabilities); len(missingTools) > 0 || len(missingCaps) > 0 {
		return errResult(fmt.Sprintf("skill requirements are not satisfied: missing_tools=%v missing_capabilities=%v", missingTools, missingCaps)), nil
	}
	content := fmt.Sprintf(
		"<skill name=%q ref=%q scope=%q>\n<resources>%s</resources>\n%s\n</skill>",
		item.Name,
		item.Ref,
		item.Scope,
		renderSkillResources(item.Resources),
		item.Body,
	)
	return textResult(content), nil
}

func (t *skillReadResourceTool) Name() string       { return "skill_read_resource" }
func (t *skillReadResourceTool) Exposure() Exposure { return ExposureDirect }
func (t *skillReadResourceTool) Description() string {
	return "Read a text resource from inside a loaded local skill package by relative path."
}
func (t *skillReadResourceTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"skill_ref":{"type":"string","description":"Exact skill ref returned by skill_search or skill_load"},
			"path":{"type":"string","description":"Relative resource path inside the skill package, such as references/api.md"}
		},
		"required":["skill_ref","path"],
		"additionalProperties":false
	}`)
}

func (t *skillReadResourceTool) Run(ctx context.Context, call Call) (Result, error) {
	var params skillReadResourceParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	item, err := t.manager.Get(
		ctx,
		ProjectIDFromContext(ctx),
		CWDFromContext(ctx),
		params.SkillRef,
	)
	if err != nil {
		return errResult("skill resource read failed: " + err.Error()), nil
	}
	if missingTools, missingCaps := skill.MissingRequirements(item, t.availableTools, t.capabilities); len(missingTools) > 0 || len(missingCaps) > 0 {
		return errResult(fmt.Sprintf("skill requirements are not satisfied: missing_tools=%v missing_capabilities=%v", missingTools, missingCaps)), nil
	}
	resource, err := t.manager.ReadResource(
		ctx,
		ProjectIDFromContext(ctx),
		CWDFromContext(ctx),
		params.SkillRef,
		params.Path,
	)
	if err != nil {
		return errResult("skill resource read failed: " + err.Error()), nil
	}
	content := fmt.Sprintf(
		"<skill_resource skill_ref=%q path=%q media_type=%q>\n%s\n</skill_resource>",
		resource.SkillRef,
		resource.Path,
		resource.MediaType,
		resource.Content,
	)
	return textResult(content), nil
}

func skillSearchScore(item skill.Skill, query string) int {
	if query == "" {
		if item.Pinned {
			return 5
		}
		return 1
	}
	hayName := strings.ToLower(item.Name)
	hayDescription := strings.ToLower(item.Description)
	hayTools := strings.ToLower(strings.Join(append(append([]string{}, item.AllowedTools...), item.RequiredTools...), "\n"))
	score := 0
	if hayName == query {
		score += 100
	}
	if strings.Contains(hayName, query) {
		score += 50
	}
	if strings.Contains(hayDescription, query) {
		score += 20
	}
	if strings.Contains(hayTools, query) {
		score += 10
	}
	for _, term := range searchTerms(query) {
		if strings.Contains(hayName, term) {
			score += 8
		}
		if strings.Contains(hayDescription, term) {
			score += 3
		}
		if strings.Contains(hayTools, term) {
			score += 2
		}
	}
	if item.Pinned {
		score += 5
	}
	return score
}

func renderSkillResources(resources []skill.Resource) string {
	if len(resources) == 0 {
		return ""
	}
	var body strings.Builder
	for index, resource := range resources {
		if index > 0 {
			body.WriteString("\n")
		}
		body.WriteString(resource.Path)
		if resource.MediaType != "" {
			body.WriteString(" ")
			body.WriteString(resource.MediaType)
		}
	}
	return body.String()
}

func textResult(text string) Result {
	return Result{Content: []ContentPart{{Type: "text", Text: text}}}
}
