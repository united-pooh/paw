package bubble

import (
	"fmt"
	"strings"

	"paw/internal/ui"
)

// DiffLine is one line of a structured line diff.
// Kind is ' ' (unchanged), '+' (added), or '-' (removed).
// Number is the old-file line number; added lines carry the line number at
// the insertion position and do not advance the counter.
type DiffLine struct {
	Kind   rune
	Number int
	Text   string
}

type lcsOp struct {
	kind rune
	text string
}

// lcsOps computes a Myers/LCS shortest edit script between a and b. It returns
// ops in forward (file) order. This is the body of the former lcsEditScript,
// minus the unused oi/ni fields.
func lcsOps(a, b []string) []lcsOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	ops := []lcsOp{}
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && a[i-1] == b[j-1]:
			ops = append(ops, lcsOp{' ', a[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			ops = append(ops, lcsOp{'+', b[j-1]})
			j--
		default:
			ops = append(ops, lcsOp{'-', a[i-1]})
			i--
		}
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}

// structuredDiff computes the LCS edit script between oldLines and newLines and
// returns ordered DiffLines carrying old-file line numbers, matching Claude
// Code's numberDiffLines strategy: unchanged/removed advance the counter; an
// added line shows the current number without advancing; a removed block
// advances through the block then rewinds so subsequent lines keep their number.
func structuredDiff(oldLines, newLines []string) []DiffLine {
	ops := lcsOps(oldLines, newLines)
	numbered := make([]DiffLine, 0, len(ops))
	lineNum := 1
	for idx := 0; idx < len(ops); {
		op := ops[idx]
		switch op.kind {
		case ' ':
			numbered = append(numbered, DiffLine{' ', lineNum, op.text})
			lineNum++
			idx++
		case '+':
			numbered = append(numbered, DiffLine{'+', lineNum, op.text})
			idx++
		case '-':
			numRemoved := 0
			for idx < len(ops) && ops[idx].kind == '-' {
				numbered = append(numbered, DiffLine{'-', lineNum, ops[idx].text})
				lineNum++
				numRemoved++
				idx++
			}
			lineNum -= numRemoved
		}
	}
	return numbered
}

// diffCounts returns the number of added and removed lines in a structured diff.
func diffCounts(lines []DiffLine) (added, removed int) {
	for _, l := range lines {
		switch l.Kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return added, removed
}

// renderDiffPreview applies a 3-line context window around changed lines,
// collapses unchanged runs with "···", formats each line with a number column,
// and caps total output length via limitDiffPreviewLines.
func renderDiffPreview(lines []DiffLine) string {
	maxLine := 0
	for _, n := range lines {
		if n.Number > maxLine {
			maxLine = n.Number
		}
	}
	width := 1
	if maxLine > 0 {
		width = len(fmt.Sprintf("%d", maxLine))
	}

	const context = 3
	visible := make([]bool, len(lines))
	for i, n := range lines {
		if n.Kind != ' ' {
			for j := maxInt(0, i-context); j <= minInt(len(lines)-1, i+context); j++ {
				visible[j] = true
			}
		}
	}

	out := []string{}
	prevVisible := false
	for i, n := range lines {
		if !visible[i] {
			prevVisible = false
			continue
		}
		if !prevVisible && i > 0 {
			out = append(out, "···")
		}
		prevVisible = true
		switch n.Kind {
		case '-':
			out = append(out, fmt.Sprintf("%*d - │ %s", width, n.Number, n.Text))
		case '+':
			out = append(out, fmt.Sprintf("%*d + │ %s", width, n.Number, n.Text))
		default:
			out = append(out, fmt.Sprintf("%*d   │ %s", width, n.Number, n.Text))
		}
	}
	return strings.Join(limitDiffPreviewLines(out), "\n")
}

// fileMutationContents extracts the legacy old/new content pair used when no
// runner snapshot is available.
func fileMutationContents(fields []toolDisplayField, oldContent string) (old, new string) {
	if fc := firstNonEmptyField(fields, "old_content", "old_string", "before"); fc != "" {
		oldContent = fc
	}
	newContent := firstNonEmptyField(fields, "new_content", "new_string", "replacement", "content", "after")
	return oldContent, newContent
}

func snapshotDiff(snapshot *ui.FileMutationSnapshot) ([]DiffLine, diffTotals, bool) {
	if snapshot == nil || (snapshot.BeforeExists == snapshot.AfterExists && snapshot.Before == snapshot.After) {
		return nil, diffTotals{}, false
	}
	lines := structuredDiff(existingContentLines(snapshot.Before, snapshot.BeforeExists), existingContentLines(snapshot.After, snapshot.AfterExists))
	added, removed := diffCounts(lines)
	if added == 0 && removed == 0 {
		return nil, diffTotals{}, false
	}
	return lines, diffTotals{added: added, removed: removed}, true
}

func existingContentLines(content string, exists bool) []string {
	if !exists || content == "" {
		return nil
	}
	return splitLines(content)
}

func previewSnapshot(name string, fields []toolDisplayField, before *ui.FileMutationSnapshot) *ui.FileMutationSnapshot {
	if before == nil {
		return nil
	}
	after, ok := anticipatedFileMutationAfter(name, fields, before.Before)
	if !ok {
		return nil
	}
	return &ui.FileMutationSnapshot{
		Before:       before.Before,
		After:        after,
		BeforeExists: before.BeforeExists,
		AfterExists:  true,
	}
}

func anticipatedFileMutationAfter(name string, fields []toolDisplayField, before string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write":
		return fieldValue(fields, "content"), true
	case "edit", "update":
		oldString := fieldValue(fields, "old_string")
		if oldString == "" {
			oldString = fieldValue(fields, "old_content")
		}
		newString := firstFieldValue(fields, "new_string", "new_content", "replacement")
		if oldString == "" || !strings.Contains(before, oldString) {
			return "", false
		}
		count := 1
		if strings.EqualFold(fieldValue(fields, "replace_all"), "true") {
			count = -1
		}
		return strings.Replace(before, oldString, newString, count), true
	default:
		return "", false
	}
}

func firstFieldValue(fields []toolDisplayField, keys ...string) string {
	for _, key := range keys {
		for _, field := range fields {
			if field.key == key {
				return field.value
			}
		}
	}
	return ""
}
