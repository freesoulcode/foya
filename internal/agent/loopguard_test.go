package agent

import (
	"encoding/json"
	"testing"
)

// 第一层:同一失败调用连续到阈值后被拦截;成功调用永不触发。
func TestLoopGate_FailStreakBlocks(t *testing.T) {
	g := &loopGuard{}
	sig := callSig("edit", json.RawMessage(`{"path":"a.go"}`))

	// 第 1、2 次失败:不拦截,照常执行。
	for i := 0; i < loopGateThreshold-1; i++ {
		if g.blockBeforeExec(sig) {
			t.Fatalf("第 %d 次相同失败调用不应被拦截", i+1)
		}
		g.recordResult(sig, true)
	}
	// 第 3 次:达阈值,拦截。
	if !g.blockBeforeExec(sig) {
		t.Fatal("连续失败达阈值后应拦截相同调用")
	}
}

func TestLoopGate_SuccessNeverBlocks(t *testing.T) {
	g := &loopGuard{}
	sig := callSig("bash", json.RawMessage(`{"command":"ls"}`))
	for i := 0; i < loopGateThreshold+3; i++ {
		if g.blockBeforeExec(sig) {
			t.Fatalf("成功调用不应被拦截(第 %d 次)", i+1)
		}
		g.recordResult(sig, false)
	}
}

// 不同参数的失败应重置流水,不累加。
func TestLoopGate_DifferentCallResetsStreak(t *testing.T) {
	g := &loopGuard{}
	a := callSig("edit", json.RawMessage(`{"path":"a.go"}`))
	b := callSig("edit", json.RawMessage(`{"path":"b.go"}`))
	g.recordResult(a, true)
	g.recordResult(b, true) // 换了调用,流水重置为 1
	if g.blockBeforeExec(a) {
		t.Fatal("换调用后原调用不应立即被拦截")
	}
	if g.failStreak != 1 {
		t.Fatalf("换调用后流水应为 1,实际 %d", g.failStreak)
	}
}

// 参数键顺序不同但等价,应视为同一签名。
func TestCallSig_KeyOrderIndependent(t *testing.T) {
	s1 := callSig("edit", json.RawMessage(`{"path":"a.go","old":"x"}`))
	s2 := callSig("edit", json.RawMessage(`{"old":"x","path":"a.go"}`))
	if s1 != s2 {
		t.Fatal("键顺序不同的等价参数应产生相同签名")
	}
}

// 第二层:同一步骤签名在窗口内超过阈值后触发硬终止。
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
		t.Fatalf("同一步骤签名出现 %d 次后应触发终止", repeatMaxCount+1)
	}
}

// 纯文本步骤(无工具调用)签名为空,不参与第二层判定。
func TestStepSig_EmptyForNoTools(t *testing.T) {
	if stepSig(nil) != "" {
		t.Fatal("无工具交互的步骤签名应为空")
	}
	g := &loopGuard{}
	for i := 0; i < repeatMaxCount+5; i++ {
		if g.recordStep("") {
			t.Fatal("空签名不应触发循环终止")
		}
	}
}
