package bubble

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"paw/internal/model"
)

func teaKeyEnter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }

func teaKeyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestNextProxyModeCycle(t *testing.T) {
	cases := []struct {
		name  string
		input *model.ProxyConfig
		want  *model.ProxyConfig
	}{
		{"nil to direct", nil, &model.ProxyConfig{Mode: model.ProxyModeDirect}},
		{"auto to direct", &model.ProxyConfig{Mode: model.ProxyModeAuto}, &model.ProxyConfig{Mode: model.ProxyModeDirect}},
		{"direct to custom", &model.ProxyConfig{Mode: model.ProxyModeDirect}, &model.ProxyConfig{Mode: model.ProxyModeCustom}},
		{"custom to nil", &model.ProxyConfig{Mode: model.ProxyModeCustom, URL: "http://127.0.0.1:7890"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextProxyMode(tc.input)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("nextProxyMode(%#v) = %#v, want nil", tc.input, got)
				}
				return
			}
			if got == nil || got.Mode != tc.want.Mode || got.URL != tc.want.URL {
				t.Fatalf("nextProxyMode(%#v) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateProxyURL(t *testing.T) {
	valid := []string{"http://127.0.0.1:7890", "https://proxy.example:8080", "socks5://localhost:1080"}
	for _, value := range valid {
		if _, err := validateProxyURL(value); err != nil {
			t.Errorf("validateProxyURL(%q) error = %v, want valid", value, err)
		}
	}
	invalid := []string{"", "   ", "not a url", "127.0.0.1:7890", "http://"}
	for _, value := range invalid {
		if _, err := validateProxyURL(value); err == nil {
			t.Errorf("validateProxyURL(%q) accepted, want error", value)
		}
	}
}

func TestProxyLabels(t *testing.T) {
	if got := proxyModeLabel(nil); got != "环境变量" {
		t.Fatalf("proxyModeLabel(nil) = %q", got)
	}
	if got := proxyModeLabel(&model.ProxyConfig{Mode: model.ProxyModeDirect}); got != "直连" {
		t.Fatalf("proxyModeLabel(direct) = %q", got)
	}
	if got := proxyModeLabel(&model.ProxyConfig{Mode: model.ProxyModeCustom, URL: "http://x"}); got != "自定义" {
		t.Fatalf("proxyModeLabel(custom) = %q", got)
	}
	if got := proxyURLLabel(nil); got != "未设置" {
		t.Fatalf("proxyURLLabel(nil) = %q", got)
	}
	if got := proxyURLLabel(&model.ProxyConfig{Mode: model.ProxyModeDirect}); got != "未设置" {
		t.Fatalf("proxyURLLabel(direct) = %q", got)
	}
	if got := proxyURLLabel(&model.ProxyConfig{Mode: model.ProxyModeCustom, URL: "http://127.0.0.1:7890"}); got != "http://127.0.0.1:7890" {
		t.Fatalf("proxyURLLabel(custom) = %q", got)
	}
	if got := providerProxyModeLabel(nil, nil); got != "继承全局（环境变量）" {
		t.Fatalf("providerProxyModeLabel(nil, nil) = %q", got)
	}
	if got := providerProxyModeLabel(&model.ProxyConfig{Mode: model.ProxyModeDirect}, nil); got != "继承全局（直连）" {
		t.Fatalf("providerProxyModeLabel(direct, nil) = %q", got)
	}
	if got := providerProxyModeLabel(nil, &model.ProxyConfig{Mode: model.ProxyModeDirect}); got != "直连" {
		t.Fatalf("providerProxyModeLabel(nil, direct) = %q", got)
	}
}

func TestConfigCenterConnectionOptionsAndEdit(t *testing.T) {
	m, _, _ := openGeneralCenter(t)
	m.configCenter.page = configCenterConnection
	m.configCenter.selected = 0

	options := m.configCenterOptions()
	if len(options) != 2 || options[0].label != "代理模式" || options[1].label != "代理地址" {
		t.Fatalf("connection options = %#v", options)
	}
	if got := options[0].description; got != "全局 · 环境变量" {
		t.Fatalf("connection mode description = %q", got)
	}
	if got := options[1].description; got != "未设置" {
		t.Fatalf("connection url description = %q", got)
	}

	// Enter 循环：环境变量 → 直连。
	m = press(m, teaKeyEnter())
	updated := m.configCenterController.Snapshot()
	if updated.Document.Proxy == nil || updated.Document.Proxy.Mode != model.ProxyModeDirect {
		t.Fatalf("global proxy after first cycle = %#v, want direct", updated.Document.Proxy)
	}

	// 再循环：直连 → 自定义。
	m = press(m, teaKeyEnter())
	updated = m.configCenterController.Snapshot()
	if updated.Document.Proxy == nil || updated.Document.Proxy.Mode != model.ProxyModeCustom {
		t.Fatalf("global proxy after second cycle = %#v, want custom", updated.Document.Proxy)
	}

	// 第三次循环回到 nil（删除全局代理字段）。
	m = press(m, teaKeyEnter())
	updated = m.configCenterController.Snapshot()
	if updated.Document.Proxy != nil {
		t.Fatalf("global proxy after third cycle = %#v, want nil", updated.Document.Proxy)
	}

	// 代理地址编辑：直接输入 URL 保存后 mode 切到 custom 并写入。
	m.configCenter.selected = 1
	m = press(m, teaKeyEnter()) // 打开 URL 编辑
	for _, r := range "http://127.0.0.1:7890" {
		m = press(m, teaKeyRune(r))
	}
	m = press(m, teaKeyEnter()) // 保存
	updated = m.configCenterController.Snapshot()
	if updated.Document.Proxy == nil || updated.Document.Proxy.Mode != model.ProxyModeCustom || updated.Document.Proxy.URL != "http://127.0.0.1:7890" {
		t.Fatalf("global proxy after URL edit = %#v", updated.Document.Proxy)
	}
}
