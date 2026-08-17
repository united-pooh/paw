package config

import (
	"context"
	"strings"
	"testing"

	"paw/internal/model"
)

// proxyObservingDiscoverer 记录最后一次收到的生效代理，验证 discoverProvider
// 把 effective proxy 注入发现器。
type proxyObservingDiscoverer struct {
	proxy *model.ProxyConfig
}

func (d *proxyObservingDiscoverer) Discover(context.Context, string, Provider, string) ([]DiscoveredModel, error) {
	return []DiscoveredModel{{Name: "live"}}, nil
}

func (d *proxyObservingDiscoverer) SetProxy(proxy *model.ProxyConfig) {
	d.proxy = model.CloneProxyConfig(proxy)
}

func TestSetProxyOperationRoundTrip(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:8000/v1"}
	document.Models["local/m"] = Model{Provider: "local", Name: "m"}
	document.ActiveModel = "local/m"
	writeManagerDocument(t, paths, document)

	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)

	proxy := &model.ProxyConfig{Mode: model.ProxyModeCustom, URL: "http://127.0.0.1:7890"}
	after, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{SetProxy(proxy)})
	if err != nil {
		t.Fatalf("SetProxy update error = %v", err)
	}
	if got := after.Document.Proxy; got == nil || got.Mode != model.ProxyModeCustom || got.URL != "http://127.0.0.1:7890" {
		t.Fatalf("document proxy after set = %#v", got)
	}

	after, err = manager.Update(context.Background(), after.Revision, []Operation{SetProxy(nil)})
	if err != nil {
		t.Fatalf("clear proxy update error = %v", err)
	}
	if after.Document.Proxy != nil {
		t.Fatalf("document proxy after clear = %#v, want nil", after.Document.Proxy)
	}
}

func TestSetYoloOperationRoundTrip(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:8000/v1"}
	document.Models["local/m"] = Model{Provider: "local", Name: "m"}
	document.ActiveModel = "local/m"
	writeManagerDocument(t, paths, document)

	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	after, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{SetYolo(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !after.Document.Yolo || !strings.Contains(string(after.Raw), `"yolo": true`) {
		t.Fatalf("yolo enable did not round trip: document=%#v raw=%s", after.Document, after.Raw)
	}
	after, err = manager.Update(context.Background(), after.Revision, []Operation{SetYolo(false)})
	if err != nil {
		t.Fatal(err)
	}
	if after.Document.Yolo || !strings.Contains(string(after.Raw), `"yolo": false`) {
		t.Fatalf("yolo disable did not persist explicitly: document=%#v raw=%s", after.Document, after.Raw)
	}
}

func TestEffectiveProxyPrecedence(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Proxy = &model.ProxyConfig{Mode: model.ProxyModeDirect}
	document.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:8000/v1"}
	document.Models["local/m"] = Model{Provider: "local", Name: "m"}
	document.ActiveModel = "local/m"
	writeManagerDocument(t, paths, document)

	manager := openTestManager(t, paths, &FakeCredentialStore{Unavailable: true}, false)
	snapshot := manager.Snapshot()
	if got := snapshot.Active.Proxy; got == nil || got.Mode != model.ProxyModeDirect {
		t.Fatalf("active proxy without provider override = %#v, want direct", got)
	}
	profile := profileByID(t, snapshot.Active.Profiles, "local")
	if got := profile.Proxy; got == nil || got.Mode != model.ProxyModeDirect {
		t.Fatalf("profile proxy without provider override = %#v, want direct", got)
	}

	provider := snapshot.Document.Providers["local"]
	provider.Proxy = &model.ProxyConfig{Mode: model.ProxyModeCustom, URL: "http://proxy.example:8080"}
	after, err := manager.Update(context.Background(), snapshot.Revision, []Operation{UpsertProvider("local", provider)})
	if err != nil {
		t.Fatalf("update provider proxy error = %v", err)
	}
	if got := after.Active.Proxy; got == nil || got.Mode != model.ProxyModeCustom || got.URL != "http://proxy.example:8080" {
		t.Fatalf("active proxy with provider override = %#v, want custom url", got)
	}
	if got := after.Document.Proxy; got == nil || got.Mode != model.ProxyModeDirect {
		t.Fatalf("global proxy must be untouched = %#v", got)
	}

	provider.Proxy = nil
	after, err = manager.Update(context.Background(), after.Revision, []Operation{UpsertProvider("local", provider)})
	if err != nil {
		t.Fatalf("clear provider proxy error = %v", err)
	}
	if got := after.Active.Proxy; got == nil || got.Mode != model.ProxyModeDirect {
		t.Fatalf("active proxy after clearing provider override = %#v, want global direct", got)
	}
}

func TestDiscovererReceivesEffectiveProxy(t *testing.T) {
	clearDetectionEnv(t)
	paths := isolatedPaths(t, false)
	document := emptyDocument()
	document.Proxy = &model.ProxyConfig{Mode: model.ProxyModeCustom, URL: "http://127.0.0.1:7890"}
	document.Providers["local"] = discoveryTestProvider("http://127.0.0.1:8000/v1")
	document.Models["local/m"] = Model{Provider: "local", Name: "m"}
	document.ActiveModel = "local/m"
	writeManagerDocument(t, paths, document)

	discoverer := &proxyObservingDiscoverer{}
	manager, err := Open(context.Background(), Options{Paths: paths, Credentials: &FakeCredentialStore{Unavailable: true}, DisableWatch: true, Discoverer: discoverer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	if got := discoverer.proxy; got == nil || got.Mode != model.ProxyModeCustom || got.URL != "http://127.0.0.1:7890" {
		t.Fatalf("discoverer proxy = %#v, want global custom url", got)
	}

	provider := manager.Snapshot().Document.Providers["local"]
	provider.Proxy = &model.ProxyConfig{Mode: model.ProxyModeDirect}
	if _, err := manager.Update(context.Background(), manager.Snapshot().Revision, []Operation{UpsertProvider("local", provider)}); err != nil {
		t.Fatal(err)
	}
	// 热重载不会重新 discovery（设计如此），discoverer 保留启动时的代理；
	// 重启后 provider 级覆盖才会生效，这里只验证 effective 解析本身。
	if got := effectiveProxy(manager.Snapshot().Document, mergePreset("local", manager.Snapshot().Document.Providers["local"])); got == nil || got.Mode != model.ProxyModeDirect {
		t.Fatalf("effective proxy after provider override = %#v, want direct", got)
	}
}

func TestEffectiveProxyFallbackToNil(t *testing.T) {
	document := emptyDocument()
	provider := Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:8000/v1"}
	if got := effectiveProxy(document, provider); got != nil {
		t.Fatalf("effective proxy without any configuration = %#v, want nil", got)
	}
}

func TestMergePresetCarriesProviderProxy(t *testing.T) {
	configured := Provider{Preset: "openrouter"}
	configured.Proxy = &model.ProxyConfig{Mode: model.ProxyModeDirect}
	merged := mergePreset("openrouter", configured)
	if got := merged.Proxy; got == nil || got.Mode != model.ProxyModeDirect {
		t.Fatalf("merged provider proxy = %#v, want direct", got)
	}
	if merged.Endpoint != "https://openrouter.ai/api/v1" {
		t.Fatalf("merged endpoint = %q, preset fields must survive", merged.Endpoint)
	}
}

func TestProxyModeNormalization(t *testing.T) {
	cases := map[string]model.ProxyMode{
		"":        model.ProxyModeAuto,
		"auto":    model.ProxyModeAuto,
		"AUTO":    model.ProxyModeAuto,
		"direct":  model.ProxyModeDirect,
		"custom":  model.ProxyModeCustom,
		"bogus":   model.ProxyModeAuto,
		"  auto ": model.ProxyModeAuto,
	}
	for raw, want := range cases {
		if got := model.NormalizeProxyMode(model.ProxyMode(raw)); got != want {
			t.Errorf("NormalizeProxyMode(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestDocumentProxySerializationRoundTrip(t *testing.T) {
	document := emptyDocument()
	document.Proxy = &model.ProxyConfig{Mode: model.ProxyModeCustom, URL: "http://127.0.0.1:7890"}
	document.Providers["local"] = Provider{Transport: TransportOpenAICompatible, Endpoint: "http://127.0.0.1:8000/v1", Proxy: &model.ProxyConfig{Mode: model.ProxyModeDirect}}
	raw, err := marshalStarter(document, "proxy round trip")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := parseAndValidateGlobal(raw, "test")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Proxy == nil || parsed.Proxy.Mode != model.ProxyModeCustom || parsed.Proxy.URL != "http://127.0.0.1:7890" {
		t.Fatalf("parsed global proxy = %#v", parsed.Proxy)
	}
	if parsed.Providers["local"].Proxy == nil || parsed.Providers["local"].Proxy.Mode != model.ProxyModeDirect {
		t.Fatalf("parsed provider proxy = %#v", parsed.Providers["local"].Proxy)
	}
	if !strings.Contains(string(raw), `"proxy"`) {
		t.Fatalf("serialized document missing proxy: %s", raw)
	}
}
