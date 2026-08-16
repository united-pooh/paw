package main

import (
	"testing"

	"paw/internal/subagent"
)

func TestParseSandboxLimits(t *testing.T) {
	limits := parseSandboxLimits("cpu=60,file_mb=128,proc=8,nofile=512,wall=300")
	want := subagent.SandboxLimits{
		CPUSeconds: 60, FileSizeMiB: 128, MaxProcesses: 8, OpenFiles: 512, JobWallSeconds: 300,
	}
	if limits != want {
		t.Fatalf("parseSandboxLimits = %#v, want %#v", limits, want)
	}
	if empty := parseSandboxLimits(""); empty != (subagent.SandboxLimits{}) {
		t.Fatalf("empty parse = %#v, want zero value", empty)
	}
	garbage := parseSandboxLimits("cpu=abc,foo=1,wall=42")
	if garbage.CPUSeconds != 0 || garbage.JobWallSeconds != 42 {
		t.Fatalf("garbage-tolerant parse = %#v, want cpu=0 wall=42", garbage)
	}
}
