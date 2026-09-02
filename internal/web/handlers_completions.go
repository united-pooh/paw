package web

import (
	"net/http"
	"strings"

	"paw/internal/complete"
	"paw/internal/skill"
)

// completionItem 是一条输入候选项：Label 为写回输入框的文本，
// Detail 为辅助说明，Dir 标记文件候选中的目录（选中后继续下钻）。
type completionItem struct {
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Dir    bool   `json:"dir,omitempty"`
}

// maxCompletionItems 限制一次返回的候选数量，弹窗一次只展示一屏。
const maxCompletionItems = 12

// webInlineCommands 是 Web 端支持的内嵌 prompt 指令（与 TUI 的
// isInlinePromptCommand 对齐）；TUI 专属界面指令（/model 等）不在此列。
var webInlineCommands = []completionItem{
	{Label: "/task", Detail: "派发子任务"},
	{Label: "/streamma", Detail: "运行 Streamma 流水线"},
}

// handleCompletions 复用 internal/complete 中与 TUI 完全相同的触发解析、
// 递归列举与前缀过滤函数，为 Web 输入框提供 @ 文件、/ 指令与 $ 技能候补。
func (s *Server) handleCompletions(writer http.ResponseWriter, request *http.Request) {
	runtime, ok := s.runtime(request)
	if !ok {
		writeJSONError(writer, http.StatusNotFound, "workspace_not_loaded", "workspace is not loaded", RequestID(request.Context()))
		return
	}
	trigger := request.URL.Query().Get("trigger")
	query := request.URL.Query().Get("query")

	switch trigger {
	case "@":
		dir, prefix := complete.ResolveSearchDir(runtime.Root, query)
		entries, err := complete.ListFilesRecursive(dir)
		if err != nil {
			entries = nil
		}
		filtered := complete.FilterByPrefix(entries, prefix)
		items := make([]completionItem, 0, min(len(filtered), maxCompletionItems))
		for _, entry := range filtered {
			items = append(items, completionItem{Label: entry, Dir: strings.HasSuffix(entry, "/")})
			if len(items) >= maxCompletionItems {
				break
			}
		}
		writeJSON(writer, http.StatusOK, struct {
			Items []completionItem `json:"items"`
		}{Items: items})

	case "/", "$":
		items := make([]completionItem, 0, maxCompletionItems)
		if trigger == "/" {
			prefix := strings.ToLower(strings.TrimSpace(query))
			for _, command := range webInlineCommands {
				if prefix == "" || strings.HasPrefix(strings.ToLower(command.Label), "/"+prefix) {
					items = append(items, command)
				}
			}
		}
		registry := skill.NewRegistry(skill.DefaultRoots(""))
		for _, sk := range registry.Find(query) {
			label := "$" + sk.Name
			if trigger == "/" {
				label = "/" + sk.Name
			}
			items = append(items, completionItem{Label: label, Detail: sk.Description})
			if len(items) >= maxCompletionItems {
				break
			}
		}
		if len(items) > maxCompletionItems {
			items = items[:maxCompletionItems]
		}
		writeJSON(writer, http.StatusOK, struct {
			Items []completionItem `json:"items"`
		}{Items: items})

	default:
		writeJSONError(writer, http.StatusBadRequest, "invalid_trigger", "trigger must be one of @, /, $", RequestID(request.Context()))
	}
}
