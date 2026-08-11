package bubble

import (
	"fmt"
	"strings"

	"paw/internal/ui"
)

// DiffLine is one line of a structured line diff.
// Kind is ' ' (unchanged), '+' (added), or '-' (removed). OldNumber and
// NewNumber are independent file coordinates; the absent side of an added or
// removed line is zero.
type DiffLine struct {
	Kind      rune
	OldNumber int
	NewNumber int
	Text      string
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
// assigns each row its independent old-file and new-file coordinates. Removed
// rows have no new coordinate, added rows have no old coordinate, and context
// rows advance both counters.
func structuredDiff(oldLines, newLines []string) []DiffLine {
	ops := lcsOps(oldLines, newLines)
	numbered := make([]DiffLine, 0, len(ops))
	oldLine, newLine := 1, 1
	for _, op := range ops {
		switch op.kind {
		case ' ':
			numbered = append(numbered, DiffLine{Kind: ' ', OldNumber: oldLine, NewNumber: newLine, Text: op.text})
			oldLine++
			newLine++
		case '+':
			numbered = append(numbered, DiffLine{Kind: '+', NewNumber: newLine, Text: op.text})
			newLine++
		case '-':
			numbered = append(numbered, DiffLine{Kind: '-', OldNumber: oldLine, Text: op.text})
			oldLine++
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
// collapses unchanged runs with "...", formats each line with independent old
// and new number columns, and caps total output length via limitDiffPreviewLines.
func renderDiffPreview(lines []DiffLine) string {
	maxOldLine, maxNewLine := 0, 0
	for _, line := range lines {
		if line.OldNumber > maxOldLine {
			maxOldLine = line.OldNumber
		}
		if line.NewNumber > maxNewLine {
			maxNewLine = line.NewNumber
		}
	}
	oldWidth := maxInt(1, len(fmt.Sprintf("%d", maxOldLine)))
	newWidth := maxInt(1, len(fmt.Sprintf("%d", maxNewLine)))

	const context = 3
	visible := make([]bool, len(lines))
	for i, line := range lines {
		if line.Kind != ' ' {
			for j := maxInt(0, i-context); j <= minInt(len(lines)-1, i+context); j++ {
				visible[j] = true
			}
		}
	}

	out := []string{}
	prevVisible := false
	for i, line := range lines {
		if !visible[i] {
			prevVisible = false
			continue
		}
		if !prevVisible && i > 0 {
			out = append(out, "...")
		}
		prevVisible = true
		oldNumber := formatDiffLineNumber(line.OldNumber, oldWidth)
		newNumber := formatDiffLineNumber(line.NewNumber, newWidth)
		switch line.Kind {
		case '-':
			out = append(out, fmt.Sprintf("%s %s - │ %s", oldNumber, newNumber, line.Text))
		case '+':
			out = append(out, fmt.Sprintf("%s %s + │ %s", oldNumber, newNumber, line.Text))
		default:
			out = append(out, fmt.Sprintf("%s %s   │ %s", oldNumber, newNumber, line.Text))
		}
	}
	return strings.Join(limitDiffPreviewLines(out), "\n")
}

func formatDiffLineNumber(number, width int) string {
	if number <= 0 {
		return strings.Repeat(" ", width)
	}
	return fmt.Sprintf("%*d", width, number)
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
