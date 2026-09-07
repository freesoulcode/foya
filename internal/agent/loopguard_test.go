package agent

import (
	"encoding/json"
	"testing"
)

// The first stage blocks an identical failing call at the threshold.
func TestLoopGate_FailStreakBlocks(t *testing.T) {
	g := &loopGuard{}
	sig := callSig("edit", json.RawMessage(`{"path":"a.go"}`))

	// The first two failures execute normally.
	for i := 0; i < loopGateThreshold-1; i++ {
		if g.blockBeforeExec(sig) {
			t.Fatalf("identical failing call %d should not be blocked", i+1)
		}
		g.recordResult(sig, true)
	}
	// The third call reaches the threshold.
	if !g.blockBeforeExec(sig) {
		t.Fatal("identical call should be blocked after repeated failures")
	}
}

func TestLoopGate_SuccessNeverBlocks(t *testing.T) {
	g := &loopGuard{}
	sig := callSig("bash", json.RawMessage(`{"command":"ls"}`))
	for i := 0; i < loopGateThreshold+3; i++ {
		if g.blockBeforeExec(sig) {
			t.Fatalf("successful call %d should not be blocked", i+1)
		}
		g.recordResult(sig, false)
	}
}

// A failure with different arguments resets the streak.
func TestLoopGate_DifferentCallResetsStreak(t *testing.T) {
	g := &loopGuard{}
	a := callSig("edit", json.RawMessage(`{"path":"a.go"}`))
	b := callSig("edit", json.RawMessage(`{"path":"b.go"}`))
	g.recordResult(a, true)
	g.recordResult(b, true) // A different call resets the streak to one.
	if g.blockBeforeExec(a) {
		t.Fatal("the original call should not be blocked after switching calls")
	}
	if g.failStreak != 1 {
		t.Fatalf("failure streak = %d, want 1 after switching calls", g.failStreak)
	}
}

// Equivalent JSON arguments produce the same signature regardless of key order.
func TestCallSig_KeyOrderIndependent(t *testing.T) {
	s1 := callSig("edit", json.RawMessage(`{"path":"a.go","old":"x"}`))
	s2 := callSig("edit", json.RawMessage(`{"old":"x","path":"a.go"}`))
	if s1 != s2 {
		t.Fatal("equivalent arguments with different key order should match")
	}
}

// The second stage stops a repeated step after the window threshold.
func TestRepeatGuard_TriggersAfterThreshold(t *testing.T) {
	g := &loopGuard{}
	step := []stepInteraction{{
		name: "bash", input: json.RawMessage(`{"command":"go build"}`), output: "same error",
	}}
	sig := stepSig(step)

	var triggered bool
	for i := 0; i <= repeatMaxCount; i++ {
		triggered = g.recordStep(sig)
	}
	if !triggered {
		t.Fatalf("step should be blocked after %d repeats", repeatMaxCount+1)
	}
}

// Text-only steps have empty signatures and do not enter the second stage.
func TestStepSig_EmptyForNoTools(t *testing.T) {
	if stepSig(nil) != "" {
		t.Fatal("a step without tool calls should have an empty signature")
	}
	g := &loopGuard{}
	for i := 0; i < repeatMaxCount+5; i++ {
		if g.recordStep("") {
			t.Fatal("an empty signature should not trigger loop protection")
		}
	}
}
