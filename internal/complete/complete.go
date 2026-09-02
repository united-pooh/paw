// Package complete 承载输入补全的纯逻辑：触发点检测、搜索目录解析、
// 递归文件列举与前缀过滤。这些函数从 TUI（internal/ui/bubble/completion.go）
// 提取而来，供 TUI 与 Web 后端共同复用，保证两端候补行为一致。
package complete

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ──────────────────────────────────────────────────────────────────────────────
// 触发检测
// ──────────────────────────────────────────────────────────────────────────────

// DetectAtTrigger 在 value 中找到最末尾的词边界 @ 触发点。
func DetectAtTrigger(value string) (atByteIndex int, query string) {
	return DetectWordTrigger(value, '@')
}

// DetectSkillTrigger 在 value 中找到最末尾的词边界 $ 触发点。
func DetectSkillTrigger(value string) (dollarByteIndex int, query string) {
	return DetectWordTrigger(value, '$')
}

// DetectWordTrigger 在 value 中找到最末尾的词边界 trigger 触发点。
// 词边界条件：触发字符位于字符串开头，或前一个字符为空白符。
// query 为触发字符之后到字符串末尾的内容；query 内不能含空白符。
// 如果未找到满足条件的触发点，返回 (-1, "")。
func DetectWordTrigger(value string, trigger rune) (byteIndex int, query string) {
	runes := []rune(value)
	n := len(runes)
	if n == 0 {
		return -1, ""
	}

	// 从末尾向前扫描，找到当前"词"的起始位置。
	wordStart := n
	for i := n - 1; i >= 0; i-- {
		if unicode.IsSpace(runes[i]) {
			wordStart = i + 1
			break
		}
		wordStart = i
	}

	if wordStart >= n {
		return -1, ""
	}
	if runes[wordStart] != trigger {
		return -1, ""
	}
	if wordStart > 0 && !unicode.IsSpace(runes[wordStart-1]) {
		return -1, ""
	}

	byteOff := 0
	for _, r := range runes[:wordStart] {
		byteOff += utf8.RuneLen(r)
	}
	return byteOff, string(runes[wordStart+1:])
}

// ──────────────────────────────────────────────────────────────────────────────
// 路径解析
// ──────────────────────────────────────────────────────────────────────────────

// ResolveSearchDir 根据 @ 之后的 query 解析搜索目录和文件名前缀，
// 相对路径基于 base（TUI 为进程 cwd，Web 为工作区根目录）：
//
//   - @       → (base, "")
//   - @foo    → (base, "foo")
//   - @sub/   → (base/sub, "")
//   - @sub/f  → (base/sub, "f")
//   - @~/f    → (HOME, "f")
//   - @/etc/f → ("/etc", "f")
func ResolveSearchDir(base, query string) (dir, prefix string) {
	if base == "" {
		base = "."
	}

	switch {
	case query == "" || query == ".":
		return base, ""

	case query == "~":
		home, _ := os.UserHomeDir()
		return home, ""

	case strings.HasPrefix(query, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return base, ""
		}
		return ResolvePathParts(home, query[2:])

	case strings.HasPrefix(query, "/"):
		return ResolvePathParts("/", query[1:])

	default:
		return ResolvePathParts(base, query)
	}
}

// ResolveSearchDirCWD 是 TUI 兼容版本：相对路径基于进程当前目录。
func ResolveSearchDirCWD(query string) (dir, prefix string) {
	cwd, _ := os.Getwd()
	return ResolveSearchDir(cwd, query)
}

// ResolvePathParts 将 base/rest 分解为 (directory, filePrefix)。
// 若 rest 含路径分隔符，最后一段作为文件名前缀，其余拼入目录。
func ResolvePathParts(base, rest string) (dir, prefix string) {
	if rest == "" {
		return base, ""
	}
	if !strings.Contains(rest, "/") {
		return base, rest
	}
	idx := strings.LastIndex(rest, "/")
	return filepath.Join(base, rest[:idx]), rest[idx+1:]
}

// ──────────────────────────────────────────────────────────────────────────────
// 递归文件列举
// ──────────────────────────────────────────────────────────────────────────────

// maxFileCompletionEntries 限制文件补全收集的条目总数，避免在 ~ 或 / 等
// 大型目录树下递归出海量结果拖慢每次击键时的过滤。
const maxFileCompletionEntries = 2000

// maxCompletionDepth 限制递归搜索的目录深度（searchDir 自身为 0 层）。
const maxCompletionDepth = 8

// completionSkipDirs 是递归遍历时整棵跳过的目录名：体积巨大且几乎不是
// 搜索目标的家目录/系统噪音目录。
var completionSkipDirs = map[string]bool{
	"node_modules": true, // 依赖目录
	"Library":      true, // macOS ~/Library、/Library
	"AppData":      true, // Windows %USERPROFILE%\AppData
	"System":       true, // macOS /System
}

// skipCompletionDir 判断递归遍历时是否应整棵跳过名为 name 的目录。
func skipCompletionDir(name string) bool {
	return completionSkipDirs[name]
}

// ListFilesRecursive 递归列出 searchDir 目录树下的全部条目（不含 searchDir
// 本身）。条目为相对 searchDir 的路径，目录以 / 结尾；completionSkipDirs
// 中的目录以及超过 maxCompletionDepth 的子树被跳过。
//
// 遍历采用逐层（BFS）方式：先收集完整的第 1 层，再第 2 层，依此类推，
// 达到 maxFileCompletionEntries 条后停止。相比深度优先遍历，BFS 保证浅层
// 目录优先占满配额。
//
// 返回的列表按嵌套深度升序排列，深度相同时保持字典序遍历顺序。
func ListFilesRecursive(searchDir string) ([]string, error) {
	var all []string
	type pending struct {
		dir   string
		depth int
	}
	queue := []pending{{dir: searchDir, depth: 0}}
	for len(queue) > 0 && len(all) < maxFileCompletionEntries {
		cur := queue[0]
		queue = queue[1:]

		entries, err := os.ReadDir(cur.dir)
		if err != nil {
			continue // 无法读取的目录（如权限不足）整棵跳过
		}
		for _, e := range entries {
			if len(all) >= maxFileCompletionEntries {
				break
			}
			name := e.Name()
			child := filepath.Join(cur.dir, name)
			if e.IsDir() {
				if skipCompletionDir(name) || cur.depth+1 > maxCompletionDepth {
					continue
				}
				queue = append(queue, pending{dir: child, depth: cur.depth + 1})
			}
			rel, err := filepath.Rel(searchDir, child)
			if err != nil {
				continue
			}
			if e.IsDir() {
				all = append(all, filepath.ToSlash(rel)+"/")
			} else {
				all = append(all, filepath.ToSlash(rel))
			}
		}
	}
	sortEntriesByDepth(all)
	return all, nil
}

// sortEntriesByDepth 按嵌套深度升序稳定排序；深度相同时保持原有的
// 字典序遍历顺序，因此同名条目中浅层（深度小）的排在前面。
func sortEntriesByDepth(items []string) {
	sort.SliceStable(items, func(i, j int) bool {
		return entryDepth(items[i]) < entryDepth(items[j])
	})
}

// entryDepth 返回条目相对路径的嵌套深度（按 / 分段数；目录的尾部斜杠
// 本身就是一个分段，因此 docs/ 与 docs/test.md 同为深度 1）。
func entryDepth(p string) int {
	return strings.Count(p, "/")
}

// ──────────────────────────────────────────────────────────────────────────────
// 前缀过滤
// ──────────────────────────────────────────────────────────────────────────────

// FilterByPrefix 筛选文件补全条目（大小写不敏感）。
//
// 文件补全使用更适合文件名搜索的匹配规则：
//   - 输入 "md" 时优先按扩展名匹配，例如 README.md；
//   - 其他输入按文件名/目录名的任意位置匹配，例如 test 命中 my_test.go；
//   - 目录仍然参与匹配，便于继续浏览目录。
func FilterByPrefix(items []string, prefix string) []string {
	if prefix == "" {
		out := make([]string, len(items))
		copy(out, items)
		return out
	}

	query := strings.ToLower(strings.TrimSpace(prefix))
	if query == "" {
		out := make([]string, len(items))
		copy(out, items)
		return out
	}

	// 对不含点号的短查询，优先把它视为扩展名；只有没有扩展名命中时，
	// 才回退到通用子串匹配。
	extensionMatches := make([]string, 0, len(items))
	if !strings.Contains(query, ".") {
		for _, item := range items {
			name := strings.TrimSuffix(item, "/")
			if strings.EqualFold(filepath.Ext(name), "."+query) {
				extensionMatches = append(extensionMatches, item)
			}
		}
		if len(extensionMatches) > 0 {
			return extensionMatches
		}
	}

	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(item), query) {
			out = append(out, item)
		}
	}
	return out
}
