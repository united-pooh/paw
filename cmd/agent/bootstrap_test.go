package main

import (
	configv2 "paw/internal/config"
	"testing"
)

func TestConfigOpenOptionsDisableDiscoveryOnlyForSubagentWorkers(t *testing.T) {
	paths := configv2.Paths{Home: "/tmp/paw"}
	if got := configOpenOptions(paths, subagentRuntimeContext{}); got.DisableModelDiscovery {
		t.Fatal("top-level discovery disabled")
	}
	if got := configOpenOptions(paths, subagentRuntimeContext{depth: 1}); !got.DisableModelDiscovery {
		t.Fatal("worker discovery enabled")
	}
}
