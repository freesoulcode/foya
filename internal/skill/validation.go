package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func unsupportedManifestFields(fields map[string]bool) []string {
	if len(fields) == 0 {
		return nil
	}
	allowed := map[string]bool{
		"name": true, "description": true, "allowed-tools": true,
		"required-tools": true, "required-capabilities": true,
		"license": true, "compatibility": true, "metadata": true,
		"category": true,
	}
	var out []string
	for field := range fields {
		if !allowed[field] {
			out = append(out, field)
		}
	}
	sort.Strings(out)
	return out
}

func diagnostic(ref, name, path, code, severity, message, field string) Diagnostic {
	return Diagnostic{
		Ref:      ref,
		Name:     name,
		Path:     path,
		Code:     code,
		Severity: severity,
		Message:  message,
		Field:    field,
	}
}

func sortDiagnostics(items []Diagnostic) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Path != items[j].Path {
			return items[i].Path < items[j].Path
		}
		if items[i].Code != items[j].Code {
			return items[i].Code < items[j].Code
		}
		return items[i].Message < items[j].Message
	})
}

func sortRejected(items []RejectedSkill) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Scope != items[j].Scope {
			return scopeRank(items[i].Scope) > scopeRank(items[j].Scope)
		}
		return items[i].Path < items[j].Path
	})
}

func MissingRequirements(item Skill, availableTools, capabilities map[string]bool) (tools, caps []string) {
	for _, name := range item.RequiredTools {
		if !availableTools[name] {
			tools = append(tools, name)
		}
	}
	for _, name := range item.RequiredCapabilities {
		if !capabilities[name] {
			caps = append(caps, name)
		}
	}
	return tools, caps
}

func IsInvocable(item Skill, availableTools, capabilities map[string]bool) bool {
	tools, caps := MissingRequirements(item, availableTools, capabilities)
	return len(tools) == 0 && len(caps) == 0
}

func skillRef(scope Scope, name string) string {
	return string(scope) + ":" + strings.ToLower(strings.TrimSpace(name))
}

func projectSkillRef(projectID, name string) string {
	return string(ScopeProject) + ":" + projectID + ":" + strings.ToLower(strings.TrimSpace(name))
}

func pluginSkillRef(pluginID, name string) string {
	return string(ScopePlugin) + ":" + pluginID + ":" + strings.ToLower(strings.TrimSpace(name))
}

func scopeRank(scope Scope) int {
	switch scope {
	case ScopeProject:
		return 4
	case ScopeGlobal:
		return 3
	case ScopePlugin:
		return 2
	default:
		return 1
	}
}
