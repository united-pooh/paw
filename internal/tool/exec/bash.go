package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"paw/internal/tool"
)

const (
	defaultTimeoutSeconds = 30
	maxOutputBytes        = 32 * 1024
)

// blockedPatterns 列出禁止执行的 shell 命令模式。
// 策略：阻止破坏性的文件删除和格式化操作，同时保留正常的开发工作流。
// 注意：这是一层防御纵深，主要防止模型意外生成危险命令；
// 真正的隔离边界是 resolveWorkingDir 的工作区路径沙箱。
var blockedPatterns = []*regexp.Regexp{
	// 递归删除（rm -rf / rm -r）
	regexp.MustCompile(`(?i)\brm\b[^;|&]*-[^\s]*r`),
	// 危险的磁盘操作
	regexp.MustCompile(`\b(mkfs|dd\b.*\bof=/dev|fdisk|parted|shred)\b`),
	// fork bomb
	regexp.MustCompile(`:\s*\(\s*\)\s*\{.*:\|:.*\}`),
}

// BashTool 在工作区内运行 shell 命令。
// 安全策略：
//  1. 正则预检，拦截高风险破坏性命令（rm -rf、mkfs 等）；
//  2. 工作目录沙箱：cwd 不允许逃出 Root；
//  3. 强制超时，超时后 kill 整个进程组。
type BashTool struct {
	Root string
}

type bashInput struct {
	Command        string `json:"command"`
	CWD            string `json:"cwd,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (t *BashTool) Name() string {
	return "Bash"
}

func (t *BashTool) Description() string {
	return "在工作区内安全执行一条 shell 命令。" +
		"破坏性命令（如 rm -rf、mkfs）会被拦截。" +
		"示例输入: {\"command\":\"go test ./...\",\"cwd\":\".\",\"timeout_seconds\":30}"
}

func (t *BashTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"cwd":{"type":"string"},"timeout_seconds":{"type":"integer","minimum":1}},"required":["command"]}`)
}

func (*BashTool) ReadOnly() bool { return false }

func (*BashTool) SnipHint() tool.SnipHint {
	return tool.SnipHint{Head: 40, Tail: 40, HeadChars: 8000, TailChars: 8000}
}

func (t *BashTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	in, err := decodeBashInput(input)
	if err != nil {
		return "", err
	}

	// 安全预检：拦截高风险命令
	if blocked, pattern := checkCommandSafety(in.Command); blocked {
		return "", fmt.Errorf("命令包含不允许的操作（匹配到危险模式: %s），执行已拦截", pattern)
	}

	workdir, err := resolveWorkingDir(t.Root, in.CWD)
	if err != nil {
		return "", err
	}

	timeout := resolveTimeout(in.TimeoutSeconds)
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := osexec.CommandContext(execCtx, "bash", "-c", in.Command)
	cmd.Dir = workdir
	cmd.Env = shellEnv(t.Root)

	var output limitedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err = cmd.Run()
	renderedOutput := output.String()

	// 超时是框架级错误，直接返回 error，让调用方决策。
	if execCtx.Err() == context.DeadlineExceeded {
		if renderedOutput == "" {
			return "", fmt.Errorf("command timed out after %s", timeout)
		}
		return "", fmt.Errorf("command timed out after %s\noutput:\n%s", timeout, renderedOutput)
	}

	// 命令本身非零退出时，把退出码附在输出末尾作为有价值的信息返回给模型，
	// 而不是作为 Go error——模型需要看到 stderr/stdout 才能判断下一步。
	if err != nil {
		exitInfo := fmt.Sprintf("\n[exit status: %s]", err.Error())
		if renderedOutput == "" {
			return exitInfo, nil
		}
		return renderedOutput + exitInfo, nil
	}

	return renderedOutput, nil
}

// checkCommandSafety 检查命令是否命中危险模式。
// 返回 (true, patternDesc) 表示命中。
func checkCommandSafety(command string) (bool, string) {
	for _, re := range blockedPatterns {
		if re.MatchString(command) {
			return true, re.String()
		}
	}
	return false, ""
}

func decodeBashInput(input json.RawMessage) (bashInput, error) {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return bashInput{}, err
	}
	if strings.TrimSpace(in.Command) == "" {
		return bashInput{}, fmt.Errorf("command is required")
	}
	return in, nil
}

func resolveTimeout(timeoutSeconds int) time.Duration {
	if timeoutSeconds <= 0 {
		return defaultTimeoutSeconds * time.Second
	}
	return time.Duration(timeoutSeconds) * time.Second
}

func resolveWorkingDir(root, cwd string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("bash tool root is empty")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}

	target := absRoot
	if strings.TrimSpace(cwd) != "" {
		if filepath.IsAbs(cwd) {
			target = cwd
		} else {
			target = filepath.Join(absRoot, cwd)
		}
	}

	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("cwd escapes workspace root: %s", cwd)
	}

	// TODO: 如果后续安全边界要更严格，这里还要补 symlink-aware 校验，防止软链接逃逸。
	return absTarget, nil
}

func shellEnv(root string) []string {
	env := os.Environ()
	home := defaultHome(root)
	if home != "" {
		env = setEnv(env, "HOME", home)
		if os.Getenv("CODEX_HOME") == "" {
			env = setEnv(env, "CODEX_HOME", filepath.Join(home, ".codex"))
		}
	}
	return env
}

func defaultHome(root string) string {
	if current, err := user.Current(); err == nil && usableHome(current.HomeDir) {
		return current.HomeDir
	}
	if home := os.Getenv("HOME"); usableHome(home) {
		return home
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	parts := strings.Split(filepath.Clean(absRoot), string(filepath.Separator))
	if len(parts) >= 3 && parts[1] == "Users" && parts[2] != "" {
		return string(filepath.Separator) + filepath.Join(parts[1], parts[2])
	}
	return ""
}

func usableHome(home string) bool {
	home = strings.TrimSpace(home)
	return home != "" && home != "/tmp"
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

type limitedBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := maxOutputBytes - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}

	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, err := b.buf.Write(p)
	return len(p), err
}

func (b *limitedBuffer) String() string {
	out := b.buf.String()
	if !b.truncated {
		return out
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out + "[output truncated]"
}
