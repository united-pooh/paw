package file

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const defaultGlobMaxResults = 200

type GlobTool struct {
	Root      string
	ReadRoots []string
}

type globInput struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

func (t *GlobTool) Name() string {
	return "Glob"
}

func (t *GlobTool) Description() string {
	return "在工作区内按 glob 模式匹配路径"
}

func (t *GlobTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"max_results":{"type":"integer","minimum":1}},"required":["pattern"]}`)
}

func (t *GlobTool) IsConcurrencySafe(json.RawMessage) bool {
	return true
}

func (t *GlobTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in globInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return "", fmt.Errorf("pattern is required")
	}

	searchRoot, err := resolvePathWithinRoots(t.Root, in.Path, t.ReadRoots)
	if err != nil {
		return "", err
	}

	re, err := compileGlobPattern(in.Pattern)
	if err != nil {
		return "", err
	}

	maxResults := in.MaxResults
	if maxResults <= 0 {
		maxResults = defaultGlobMaxResults
	}

	matches := make([]string, 0, min(maxResults, 16))
	err = filepath.WalkDir(searchRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == searchRoot {
			return nil
		}

		relToSearch := filepath.ToSlash(relativePath(searchRoot, path))
		if !re.MatchString(relToSearch) {
			return nil
		}

		matches = append(matches, displayPath(t.Root, path))
		if len(matches) >= maxResults {
			return errGrepLimitReached
		}
		return nil
	})
	if err != nil && err != errGrepLimitReached {
		return "", err
	}
	if len(matches) == 0 {
		return "no matches", nil
	}

	sort.Strings(matches)
	return strings.Join(matches, "\n"), nil
}

func compileGlobPattern(pattern string) (*regexp.Regexp, error) {
	pattern = filepath.ToSlash(pattern)

	var expr strings.Builder
	expr.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch == '*' {
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				expr.WriteString(".*")
				i++
				continue
			}
			expr.WriteString(`[^/]*`)
			continue
		}
		if ch == '?' {
			expr.WriteString(`[^/]`)
			continue
		}
		expr.WriteString(regexp.QuoteMeta(string(ch)))
	}
	expr.WriteString("$")

	return regexp.Compile(expr.String())
}
