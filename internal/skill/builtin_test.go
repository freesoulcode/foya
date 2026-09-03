package skill

import "testing"

func TestBuiltinSkillsAreEmpty(t *testing.T) {
	if got := BuiltinDefinitions(); len(got) != 0 {
		t.Fatalf("builtin skills = %#v, want none", got)
	}
}
