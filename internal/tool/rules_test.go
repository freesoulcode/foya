package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/contextdata"
)

func TestRuleLoadOnlyLoadsModelDecisionRules(t *testing.T) {
	store, err := contextdata.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	modelRule, err := store.CreateRule(
		contextdata.ScopeGlobal,
		"",
		"Use transactional migrations.",
		contextdata.RuleOptions{
			Name:        "migrations",
			Description: "Database migration constraints.",
			Trigger:     contextdata.RuleModelDecision,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRule(
		contextdata.ScopeGlobal,
		"",
		"Only when explicitly requested.",
		contextdata.RuleOptions{Name: "review", Trigger: contextdata.RuleManual},
	); err != nil {
		t.Fatal(err)
	}

	loader := NewRuleLoadTool(store)
	result, err := loader.Run(
		context.Background(),
		Call{Input: []byte(`{"name":"migrations"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 ||
		!strings.Contains(result.Content[0].Text, modelRule.Content) {
		t.Fatalf("result = %#v", result)
	}

	result, err = loader.Run(
		context.Background(),
		Call{Input: []byte(`{"name":"review"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("manual rule unexpectedly loaded: %#v", result)
	}
}
