package conversation

import "testing"

const validSummary = `## Goal
Finish the context runtime.
## Progress
The planner is implemented.
## Key Decisions
Keep canonical events immutable.
## Next Steps
Run the integration tests.
## Critical Context
The checkpoint table is a derived projection.`

func TestValidateSummary(t *testing.T) {
	if err := ValidateSummary(validSummary); err != nil {
		t.Fatalf("valid summary rejected: %v", err)
	}

	for name, value := range map[string]string{
		"empty section": `## Goal
## Progress
Done.
## Key Decisions
Keep events.
## Next Steps
Test.
## Critical Context
None.`,
		"out of order": `## Progress
Done.
## Goal
Finish.
## Key Decisions
Keep events.
## Next Steps
Test.
## Critical Context
None.`,
		"unexpected section": `## Goal
Finish.
## Progress
Done.
## Notes
Extra.
## Key Decisions
Keep events.
## Next Steps
Test.
## Critical Context
None.`,
		"unclosed fence": validSummary + "\n```text\nunfinished",
		"truncated":      validSummary + "\n[truncated]",
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateSummary(value); err == nil {
				t.Fatalf("invalid summary accepted: %s", value)
			}
		})
	}
}

func TestOutputTokenLimit(t *testing.T) {
	if got := OutputTokenLimit(0); got != DefaultOutputTokens {
		t.Fatalf("default output limit = %d", got)
	}
	if got := OutputTokenLimit(2_048); got != 2_048 {
		t.Fatalf("configured lower output limit = %d", got)
	}
	if got := OutputTokenLimit(32_000); got != MaxOutputTokens {
		t.Fatalf("capped output limit = %d", got)
	}
}
