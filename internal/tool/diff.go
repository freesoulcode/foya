package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"strings"

	"github.com/freesoulcode/foya/internal/message"
)

func trackedFileChange(
	path string,
	before []byte,
	beforeExists bool,
	beforeMode fs.FileMode,
	after []byte,
	afterMode fs.FileMode,
) *message.FileChange {
	change := &message.FileChange{
		Path:            path,
		BeforeExists:    beforeExists,
		BeforeMode:      uint32(beforeMode.Perm()),
		AfterMode:       uint32(afterMode.Perm()),
		AfterBlob:       contentSHA256(after),
		BeforeContent:   append([]byte(nil), before...),
		AfterContent:    append([]byte(nil), after...),
		ContentCaptured: true,
	}
	if beforeExists {
		change.BeforeBlob = contentSHA256(before)
	}
	return change
}

func contentSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// UnifiedDiff 生成 old→new 的统一 diff 文本(供 UI 行内展示)。
// 采用行级 LCS 计算最小编辑,输出带 @@ hunk 头、以 ' '/'+'/'-' 前缀的行,
// 与常见 unified diff 兼容,便于前端按前缀着色。path 用于 ---/+++ 文件头。
// 无变化时返回空串。
func UnifiedDiff(path, oldText, newText string) string {
	if oldText == newText {
		return ""
	}
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	ops := diffLines(oldLines, newLines)

	hunks := groupHunks(ops, 3)
	if len(hunks) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n", path)
	fmt.Fprintf(&b, "+++ %s\n", path)
	for _, h := range hunks {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.oldStart, h.oldCount, h.newStart, h.newCount)
		for _, ln := range h.lines {
			b.WriteString(ln)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// splitLines 按 \n 拆分,保留结尾空行语义(末尾换行不产生多余空行)。
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// 末尾换行会产生一个尾随空串,去掉以避免虚假的空行差异。
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type diffOp struct {
	kind byte // ' ' 相等, '-' 删除, '+' 新增
	text string
}

// diffLines 用行级 LCS 计算最小编辑序列。
func diffLines(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// LCS 长度表。
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		} else if lcs[i+1][j] >= lcs[i][j+1] {
			ops = append(ops, diffOp{'-', a[i]})
			i++
		} else {
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

type hunk struct {
	oldStart, oldCount int
	newStart, newCount int
	lines              []string
}

// groupHunks 把编辑序列按变化点聚合成 hunk,每个变化点前后保留 ctx 行上下文。
func groupHunks(ops []diffOp, ctx int) []hunk {
	// 标记哪些位置需要保留(变化行及其上下文)。
	keep := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind != ' ' {
			lo := i - ctx
			if lo < 0 {
				lo = 0
			}
			hi := i + ctx
			if hi >= len(ops) {
				hi = len(ops) - 1
			}
			for k := lo; k <= hi; k++ {
				keep[k] = true
			}
		}
	}

	var hunks []hunk
	oldLine, newLine := 1, 1
	i := 0
	for i < len(ops) {
		if !keep[i] {
			if ops[i].kind != '+' {
				oldLine++
			}
			if ops[i].kind != '-' {
				newLine++
			}
			i++
			continue
		}
		h := hunk{oldStart: oldLine, newStart: newLine}
		for i < len(ops) && keep[i] {
			op := ops[i]
			h.lines = append(h.lines, string(op.kind)+op.text)
			switch op.kind {
			case ' ':
				h.oldCount++
				h.newCount++
				oldLine++
				newLine++
			case '-':
				h.oldCount++
				oldLine++
			case '+':
				h.newCount++
				newLine++
			}
			i++
		}
		hunks = append(hunks, h)
	}
	return hunks
}
