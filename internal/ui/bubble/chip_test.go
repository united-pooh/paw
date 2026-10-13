package bubble

import (
	"strings"
	"testing"
)

func TestChipKindForTool(t *testing.T) {
	cases := map[string]chipKind{
		"Read":              chipKindRead,
		"read":              chipKindRead,
		"Edit":              chipKindEdit,
		"Write":             chipKindWrite,
		"Bash":              chipKindShell,
		"Grep":              chipKindSearch,
		"Glob":              chipKindSearch,
		"LS":                chipKindSearch,
		"search_transcript": chipKindSearch,
		"WebFetch":          chipKindWeb,
		"update_todo":       chipKindTodo,
		"update_memory":     chipKindMemo,
		"codegraph__search": chipKindPlugin,
		"unknown_tool":      chipKindTool,
		"":                  chipKindTool,
	}
	for name, want := range cases {
		if got := chipKindForTool(name); got != want {
			t.Errorf("chipKindForTool(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestChipLabelsChinese(t *testing.T) {
	for _, kind := range []chipKind{chipKindRead, chipKindEdit, chipKindWrite, chipKindShell, chipKindSearch, chipKindWeb, chipKindTask, chipKindTodo, chipKindMemo, chipKindPlugin, chipKindTool} {
		if strings.TrimSpace(kind.label()) == "" {
			t.Errorf("chipKind %d 标签为空", kind)
		}
	}
	if chipKindRead.label() != "读取" || chipKindEdit.label() != "编辑" || chipKindShell.label() != "Shell" {
		t.Errorf("核心 chip 标签不符合文案表: %q %q %q", chipKindRead.label(), chipKindEdit.label(), chipKindShell.label())
	}
}

func TestChipColorSharing(t *testing.T) {
	if chipKindEdit.colorRole() != chipKindWrite.colorRole() {
		t.Error("写入与编辑应同色（同类同色）")
	}
	if chipKindSearch.colorRole() != chipKindWeb.colorRole() {
		t.Error("网页与搜索应同色（同类同色）")
	}
}

func TestRenderStatusText(t *testing.T) {
	if got := statusTextLabel("error"); got != "失败" {
		t.Errorf("error 应渲染为 失败, got %q", got)
	}
	if got := statusTextLabel("ok"); got != "完成" {
		t.Errorf("ok 应渲染为 完成, got %q", got)
	}
	out := renderStatusText("ok", "1.2s")
	if !strings.Contains(out, "完成") || !strings.Contains(out, "1.2s") {
		t.Errorf("renderStatusText 输出缺标签或耗时: %q", out)
	}
}
