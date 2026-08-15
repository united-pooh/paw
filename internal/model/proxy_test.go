package model

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// TestHTTPClientWithProxyModeSelection 验证模式选择逻辑：direct 与非法 custom
// 得到 nil Proxy（强制直连），auto/nil/custom 得到非 nil Proxy 函数。具体
// 代理地址由环境或配置决定，这里不依赖进程环境值（ProxyFromEnvironment 在
// 进程内只解析一次，无法被测试可靠改写）。
func TestHTTPClientWithProxyModeSelection(t *testing.T) {
	client := httpClientWithProxy(&ProxyConfig{Mode: ProxyModeAuto})
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("auto transport proxy must be ProxyFromEnvironment")
	}

	client = httpClientWithProxy(nil)
	transport, _ = client.Transport.(*http.Transport)
	if transport.Proxy == nil {
		t.Fatal("nil proxy transport must use ProxyFromEnvironment")
	}

	client = httpClientWithProxy(&ProxyConfig{Mode: ProxyModeDirect})
	transport, _ = client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatal("direct transport proxy must be nil")
	}

	client = httpClientWithProxy(&ProxyConfig{Mode: ProxyModeCustom, URL: "http://127.0.0.1:7890"})
	transport, _ = client.Transport.(*http.Transport)
	if transport.Proxy == nil {
		t.Fatal("custom transport proxy must be set")
	}

	client = httpClientWithProxy(&ProxyConfig{Mode: ProxyModeCustom, URL: "not a url"})
	transport, _ = client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatal("invalid custom transport proxy must fall back to nil")
	}
}

// TestHTTPClientWithProxyRoutesTraffic 端到端验证：custom 模式请求真正经过
// 指定代理转发，direct 模式请求直连目标。
func TestHTTPClientWithProxyRoutesTraffic(t *testing.T) {
	var targetHits, proxyHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	reverseProxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: strings.TrimPrefix(target.URL, "http://")})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		reverseProxy.ServeHTTP(w, r)
	}))
	defer proxy.Close()

	client := httpClientWithProxy(&ProxyConfig{Mode: ProxyModeCustom, URL: proxy.URL})
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("custom proxy request error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if proxyHits.Load() == 0 || targetHits.Load() == 0 {
		t.Fatalf("custom proxy routing: proxyHits=%d targetHits=%d, want both > 0", proxyHits.Load(), targetHits.Load())
	}

	directClient := httpClientWithProxy(&ProxyConfig{Mode: ProxyModeDirect})
	resp, err = directClient.Get(target.URL)
	if err != nil {
		t.Fatalf("direct request error = %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if proxyHits.Load() != 1 {
		t.Fatalf("direct routing must bypass proxy, proxyHits=%d want 1", proxyHits.Load())
	}
}

func TestApplyModelConfigRebuildsTransport(t *testing.T) {
	client := NewClient(Config{APIBaseURL: "http://127.0.0.1:8000/v1", Model: "m"})
	before := client.httpClient.Transport

	if err := client.ApplyModelConfig(Config{
		APIBaseURL: "http://127.0.0.1:8000/v1",
		Model:      "m",
		Proxy:      &ProxyConfig{Mode: ProxyModeDirect},
	}); err != nil {
		t.Fatalf("ApplyModelConfig error = %v", err)
	}
	if client.httpClient.Transport == before {
		t.Fatal("transport must be rebuilt after proxy change")
	}
	transport, ok := client.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.httpClient.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("transport proxy after direct must be nil")
	}
}

func TestProxyConfigClone(t *testing.T) {
	original := &ProxyConfig{Mode: ProxyModeCustom, URL: "http://127.0.0.1:7890"}
	cloned := CloneProxyConfig(original)
	cloned.URL = "http://other:1"
	if original.URL != "http://127.0.0.1:7890" {
		t.Fatalf("clone mutated original: %#v", original)
	}
	if CloneProxyConfig(nil) != nil {
		t.Fatal("CloneProxyConfig(nil) must be nil")
	}
}
