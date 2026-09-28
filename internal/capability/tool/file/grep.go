package file

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

const (
	defaultGrepMaxResults = 200
	maxGrepLineBytes      = 1024 * 1024
	maxGrepWarnings       = 10
)

type GrepTool struct {
	Root      string
	ReadRoots []string
}

type grepInput struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	Literal    bool   `json:"literal,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

func (t *GrepTool) Name() string {
	return "Grep"
}

func (t *GrepTool) Description() string {
	return "在工作区内按内容搜索文本，返回匹配的文件、行号和内容"
}

func (t *GrepTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"literal":{"type":"boolean"},"max_results":{"type":"integer","minimum":1}},"required":["pattern"]}`)
}

func (t *GrepTool) IsConcurrencySafe(json.RawMessage) bool {
	return true
}

func (t *GrepTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in grepInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return "", fmt.Errorf("pattern is required")
	}

	searchRoot, policy, err := resolveReadPathWithinRoots(t.Root, in.Path, t.ReadRoots)
	if err != nil {
		return "", err
	}

	matcher, err := buildLineMatcher(in.Pattern, in.Literal)
	if err != nil {
		return "", err
	}

	maxResults := in.MaxResults
	if maxResults <= 0 {
		maxResults = defaultGrepMaxResults
	}

	var results []string
	var warnings []string
	skippedLines := 0
	err = walkReadPaths(ctx, searchRoot, policy, func(path string, entry fs.DirEntry) error {
		if entry.IsDir() {
			return nil
		}
		matches, err := grepFile(ctx, t.Root, path, matcher, maxResults-len(results), func(line int) {
			skippedLines++
			if len(warnings) < maxGrepWarnings {
				warnings = append(warnings, fmt.Sprintf("- %s:%d: line exceeds %d bytes", displayPath(t.Root, path), line, maxGrepLineBytes))
			}
		})
		if err != nil {
			return err
		}
		results = append(results, matches...)
		if len(results) >= maxResults {
			return errGrepLimitReached
		}
		return nil
	})
	if err != nil && err != errGrepLimitReached {
		return "", err
	}
	output := strings.Join(results, "\n")
	if output == "" {
		output = "no matches"
	}
	if skippedLines > 0 {
		output += fmt.Sprintf("\n\nWarning: skipped %d oversized lines (limit %d bytes per line); search results may be incomplete.\n", skippedLines, maxGrepLineBytes)
		output += strings.Join(warnings, "\n")
		if omitted := skippedLines - len(warnings); omitted > 0 {
			output += fmt.Sprintf("\n- %d additional oversized lines omitted", omitted)
		}
	}
	return output, nil
}

type lineMatcher func(string) bool

func buildLineMatcher(pattern string, literal bool) (lineMatcher, error) {
	if literal {
		return func(line string) bool {
			return strings.Contains(line, pattern)
		}, nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return re.MatchString, nil
}

var errGrepLimitReached = fmt.Errorf("grep max results reached")

func grepFile(ctx context.Context, workspaceRoot, path string, matcher lineMatcher, limit int, skipped func(int)) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	reader := bufio.NewReaderSize(f, 64*1024)

	relPath := displayPath(workspaceRoot, path)
	matches := make([]string, 0, min(limit, 8))
	lineNo := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, oversized, err := readGrepLine(ctx, reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		lineNo++
		if oversized {
			skipped(lineNo)
			continue
		}
		if !matcher(line) {
			continue
		}
		matches = append(matches, fmt.Sprintf("%s:%d:%s", relPath, lineNo, line))
		if len(matches) >= limit {
			return matches, nil
		}
	}
	return matches, nil
}

func readGrepLine(ctx context.Context, reader *bufio.Reader) (string, bool, error) {
	var line []byte
	oversized := false
	for {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		fragment, err := reader.ReadSlice('\n')
		if err == io.EOF && len(fragment) == 0 && len(line) == 0 && !oversized {
			return "", false, io.EOF
		}
		if len(fragment) > 0 && fragment[len(fragment)-1] == '\n' {
			fragment = fragment[:len(fragment)-1]
		}
		if !oversized {
			if len(line)+len(fragment) > maxGrepLineBytes {
				oversized = true
				line = nil
			} else {
				line = append(line, fragment...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil && err != io.EOF {
			return "", false, err
		}
		return strings.TrimSuffix(string(line), "\r"), oversized, nil
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
