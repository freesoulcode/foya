// 循环防护:单回合内检测 agent 是否陷入无意义重复,分两层处理。
//
// 第一层(失败门控):同一工具+相同参数连续失败达阈值时,软拦截后续相同调用,
// 不再执行,而是回灌一段引导文本让模型换做法——回合继续。成功调用永不拦截
// (允许正常轮询/重试)。
//
// 第二层(重复兜底):滑动窗口内同一「工具+参数+输出」步骤签名出现过多,判定为
// 无进展循环,硬终止回合并向用户说明原因。兜第一层抓不到的「成功但空转」场景。
package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	// loopGateThreshold 是第一层阈值:同一调用连续失败前 threshold-1 次照常执行,
	// 第 threshold 次起拦截。3 意味着「失败两次后,第三次相同调用被拦」。
	loopGateThreshold = 3
	// repeatWindowSize / repeatMaxCount 是第二层参数:近 repeatWindowSize 个步骤内,
	// 同一签名出现超过 repeatMaxCount 次即终止。窗口取较宽的阈值,
	// 降低误杀合法轮询的概率。
	repeatWindowSize = 20
	repeatMaxCount   = 8
)

// loopGuard 持有单回合的循环检测状态。非并发安全:仅在单回合的执行 goroutine 内使用。
type loopGuard struct {
	// 第一层:连续失败流水。
	lastFailSig string
	failStreak  int
	// 第二层:近期步骤签名滑动窗口。
	window []string
}

// blockBeforeExec 在执行工具前判断是否拦截该调用(第一层)。
// 当该调用与最近连续失败的调用完全相同且已达阈值时返回 true。
func (g *loopGuard) blockBeforeExec(sig string) bool {
	return sig == g.lastFailSig && g.failStreak >= loopGateThreshold-1
}

// recordResult 在工具执行(或被拦截)后更新失败流水(第一层)。
// 失败且与上次相同则累加;换了调用或成功则重置。
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

// recordStep 记录一个步骤的交互签名,返回是否已陷入无进展循环(第二层)。
// 空签名(纯文本步骤,无工具调用)不参与判定。
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

// stepInteraction 是第二层签名的输入:一次工具调用及其输出。
type stepInteraction struct {
	name   string
	input  json.RawMessage
	output string
}

// callSig 计算「工具名+规范化参数」签名(第一层用)。参数经 JSON 归一化,
// 使键顺序不同但等价的参数产生相同签名。
func callSig(name string, input json.RawMessage) string {
	return name + "\x00" + canonicalJSON(input)
}

// stepSig 计算一个步骤所有工具交互的签名(第二层用,含输出)。
// 无工具调用时返回空串——纯文本回复不参与循环判定。
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

// canonicalJSON 归一化 JSON:解析后重新序列化。Go 的 json.Marshal 会对 map 键排序,
// 因此等价对象产生稳定输出。解析失败时回退原始字节。
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

// loopGateText 是第一层拦截时回灌给模型的可恢复错误文本,引导其改变做法。
func loopGateText(toolName string) string {
	return "已拦截:该 " + toolName + " 调用(参数完全相同)已连续失败多次,重试结果不会改变," +
		"因此未再执行。请更换参数,或先采取其它步骤(例如先读取相关文件、检查当前状态)再重试。"
}
