package report

import (
	"fmt"
	"strings"
)

// maxDiffLines bounds the LCS table. Dockerfiles are tens of lines; past this
// the diff degrades to "replace everything", which is still correct output.
const maxDiffLines = 2000

// Diff returns a unified diff of a against b with the given context lines.
// An empty string means the inputs are identical.
func Diff(aName, bName, a, b string, context int) string {
	al := splitLines(a)
	bl := splitLines(b)
	ops := diffOps(al, bl)

	same := true
	for _, op := range ops {
		if op.kind != ' ' {
			same = false
			break
		}
	}
	if same {
		return ""
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", aName, bName)

	// Group ops into hunks separated by more than 2*context unchanged lines.
	i := 0
	for i < len(ops) {
		for i < len(ops) && ops[i].kind == ' ' {
			i++
		}
		if i >= len(ops) {
			break
		}
		start := i - context
		if start < 0 {
			start = 0
		}
		end := i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run-end > 2*context || run == len(ops) {
				end += min(context, run-end)
				break
			}
			end = run
		}
		writeHunk(&out, ops[start:end])
		i = end
	}
	return out.String()
}

type op struct {
	kind         byte // ' ', '-', '+'
	text         string
	aLine, bLine int // 1-based positions, for hunk headers
}

func writeHunk(out *strings.Builder, ops []op) {
	aStart, bStart, aCount, bCount := 0, 0, 0, 0
	for _, o := range ops {
		if o.kind != '+' {
			if aStart == 0 {
				aStart = o.aLine
			}
			aCount++
		}
		if o.kind != '-' {
			if bStart == 0 {
				bStart = o.bLine
			}
			bCount++
		}
	}
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", aStart, aCount, bStart, bCount)
	for _, o := range ops {
		out.WriteByte(o.kind)
		out.WriteString(o.text)
		out.WriteByte('\n')
	}
}

// diffOps computes an edit script by longest common subsequence.
func diffOps(a, b []string) []op {
	if len(a) > maxDiffLines || len(b) > maxDiffLines {
		var ops []op
		for i, l := range a {
			ops = append(ops, op{kind: '-', text: l, aLine: i + 1})
		}
		for i, l := range b {
			ops = append(ops, op{kind: '+', text: l, bLine: i + 1})
		}
		return ops
	}
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{kind: ' ', text: a[i], aLine: i + 1, bLine: j + 1})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{kind: '-', text: a[i], aLine: i + 1, bLine: j + 1})
			i++
		default:
			ops = append(ops, op{kind: '+', text: b[j], aLine: i + 1, bLine: j + 1})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{kind: '-', text: a[i], aLine: i + 1, bLine: j + 1})
	}
	for ; j < m; j++ {
		ops = append(ops, op{kind: '+', text: b[j], aLine: i + 1, bLine: j + 1})
	}
	return ops
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
