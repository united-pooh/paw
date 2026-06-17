package file

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const defaultGrepMaxResults = 200

type GrepTool struct {
	Root string
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

	searchRoot, err := resolvePathWithinRoot(t.Root, in.Path)
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
	err = walkSearchFiles(searchRoot, func(path string) error {
		matches, err := grepFile(ctx, t.Root, path, matcher, maxResults-len(results))
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
	if len(results) == 0 {
		return "no matches", nil
	}
	return strings.Join(results, "\n"), nil
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

func walkSearchFiles(root string, visit func(path string) error) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return visit(root)
	}

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		return visit(path)
	})
}

func grepFile(ctx context.Context, workspaceRoot, path string, matcher lineMatcher, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	relPath := relativePath(workspaceRoot, path)
	matches := make([]string, 0, min(limit, 8))
	lineNo := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lineNo++
		line := scanner.Text()
		if !matcher(line) {
			continue
		}
		matches = append(matches, fmt.Sprintf("%s:%d:%s", relPath, lineNo, line))
		if len(matches) >= limit {
			return matches, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return matches, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
