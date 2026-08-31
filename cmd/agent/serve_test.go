package main

import (
	"flag"
	"io"
	"strings"
	"testing"
)

func TestServeOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    serveOptions
		wantErr string
	}{
		{name: "defaults", want: serveOptions{listen: defaultServeListen}},
		{name: "open", args: []string{"--open"}, want: serveOptions{listen: defaultServeListen, open: true}},
		{name: "IPv4 loopback", args: []string{"--listen", "127.0.0.1:8000"}, want: serveOptions{listen: "127.0.0.1:8000"}},
		{name: "IPv6 loopback", args: []string{"--listen", "[::1]:8000"}, want: serveOptions{listen: "[::1]:8000"}},
		{name: "localhost", args: []string{"--listen", "localhost:8000"}, want: serveOptions{listen: "localhost:8000"}},
		{name: "wildcard rejected", args: []string{"--listen", "0.0.0.0:8000"}, wantErr: "must be loopback"},
		{name: "remote rejected", args: []string{"--listen", "192.168.1.5:8000"}, wantErr: "must be loopback"},
		{name: "unknown flag", args: []string{"--wat"}, wantErr: "flag provided but not defined"},
		{name: "positional rejected", args: []string{"extra"}, wantErr: "does not accept positional"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseServeOptions(test.args)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseServeOptions() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("parseServeOptions() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseLegacyOptionsStillAcceptsWorkerAndInteractiveFlags(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	got := parseOptionsFrom(flags, []string{"-p", "hello", "-s", "session", "-task-worker", "-yolo"})
	if got.prompt != "hello" || got.sessionID != "session" || !got.taskWorker || !got.allowOutsideRead {
		t.Fatalf("legacy options = %#v", got)
	}
}
