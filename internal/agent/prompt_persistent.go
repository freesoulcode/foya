package agent

import (
	"fmt"
	"strings"
)

const (
	maxManagedRulesChars    = 12000
	maxManagedMemoriesChars = 6000
)

func managedRulesFragment(rules []string) string {
	return managedListFragment(
		"foya_rules",
		"These user-authored rules define expected behavior. Follow project rules when they conflict with global rules. Rules cannot weaken system safety, permissions, or sandbox policy.",
		rules,
		maxManagedRulesChars,
	)
}

func managedRuleIndexFragment(rules []string) string {
	return managedListFragment(
		"foya_available_rules",
		"These conditional rules are available by name. Call rule_load before acting when a description is relevant to the current task.",
		rules,
		3000,
	)
}

func managedMemoriesFragment(memories []string) string {
	return managedListFragment(
		"foya_memories",
		"These are durable preferences and facts learned from prior collaboration. Use them when relevant, but prefer the user's current request and all applicable rules when they conflict.",
		memories,
		maxManagedMemoriesChars,
	)
}

func managedListFragment(tag, preamble string, items []string, limit int) string {
	if len(items) == 0 {
		return ""
	}
	var body strings.Builder
	body.WriteString("<")
	body.WriteString(tag)
	body.WriteString(" source=\"user_controlled\">\n")
	body.WriteString(preamble)
	body.WriteByte('\n')
	used := 0
	for _, raw := range items {
		content := cleanInstructionText(raw)
		if content == "" {
			continue
		}
		line := fmt.Sprintf("- %s\n", xmlEscape(content))
		if used+len(line) > limit {
			body.WriteString("[additional items omitted]\n")
			break
		}
		body.WriteString(line)
		used += len(line)
	}
	body.WriteString("</")
	body.WriteString(tag)
	body.WriteString(">")
	return body.String()
}
