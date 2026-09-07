// Loop protection detects repeated work within a turn in two stages.
//
// The failure gate blocks an identical call after repeated failures and asks
// the model to change approach. Successful calls never trigger this gate.
//
// The repetition fallback stops a turn when identical tool interactions occur
// too often in a sliding window, including successful calls with no progress.
package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	// loopGateThreshold blocks the third identical call after two failures.
	loopGateThreshold = 3
	// Repetition limits use a broad window to avoid blocking valid polling.
	repeatWindowSize = 20
	repeatMaxCount   = 8
)

// loopGuard tracks one turn and is not safe for concurrent use.
type loopGuard struct {
	// Consecutive failures for the first-stage gate.
	lastFailSig string
	failStreak  int
	// Recent step signatures for the second-stage fallback.
	window []string
}

// blockBeforeExec checks the repeated-failure gate before tool execution.
func (g *loopGuard) blockBeforeExec(sig string) bool {
	return sig == g.lastFailSig && g.failStreak >= loopGateThreshold-1
}

// recordResult updates the consecutive-failure state.
func (g *loopGuard) recordResult(sig string, isErr bool) {
	if !isErr {
		g.lastFailSig = ""
		g.failStreak = 0
		return
	}
	if sig == g.lastFailSig {
		g.failStreak++
		return
	}
	g.lastFailSig = sig
	g.failStreak = 1
}

// recordStep reports whether a tool interaction is repeating without progress.
func (g *loopGuard) recordStep(sig string) bool {
	if sig == "" {
		return false
	}
	g.window = append(g.window, sig)
	if len(g.window) > repeatWindowSize {
		g.window = g.window[len(g.window)-repeatWindowSize:]
	}
	count := 0
	for _, s := range g.window {
		if s == sig {
			count++
		}
	}
	return count > repeatMaxCount
}

// stepInteraction is the input to a second-stage signature.
type stepInteraction struct {
	name   string
	input  json.RawMessage
	output string
}

// callSig hashes a tool name and canonical arguments for the failure gate.
func callSig(name string, input json.RawMessage) string {
	return name + "\x00" + canonicalJSON(input)
}

// stepSig hashes all tool interactions and outputs in one model step.
func stepSig(calls []stepInteraction) string {
	if len(calls) == 0 {
		return ""
	}
	h := sha256.New()
	for _, c := range calls {
		h.Write([]byte(c.name))
		h.Write([]byte{0})
		h.Write([]byte(canonicalJSON(c.input)))
		h.Write([]byte{0})
		h.Write([]byte(c.output))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalJSON normalizes object key order and falls back to the raw input.
func canonicalJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

// loopGateText asks the model to recover with a different approach.
func loopGateText(toolName string) string {
	return "Blocked: the " + toolName + " call with identical arguments has failed repeatedly; retrying will not change the result. " +
		"Change the arguments or take another step, such as reading relevant files or checking current state, before retrying."
}
