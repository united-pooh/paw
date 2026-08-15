package main

import (
	"flag"
	"os"
	"strconv"
	"strings"
)

type options struct {
	prompt             string
	sessionID          string
	subagentWorker     bool
	subagentWorkerPool bool
	streamMA           bool
	tokenTracer        bool
	tokenTracerOpen    bool
	tokenTracerPort    int
	allowOutsideRead   bool
}

func parseOptions() options {
	prompt := flag.String("p", "", "single-turn prompt; omit to start Bubble Tea UI")
	sessionID := flag.String("s", "", "session ID; omit to resume/create by cwd")
	subagentWorker := flag.Bool("subagent-worker", false, "run hidden subagent worker")
	subagentWorkerPool := flag.Bool("subagent-worker-pool", false, "run hidden long-lived subagent pool worker")
	streamMA := flag.Bool("streamma", defaultStreamMAEnabled(), "enable /streamma and /streamma-trace commands")
	tokenTracer := flag.Bool("token-tracer", defaultTokenTracerEnabled(), "start local Token Tracer dashboard in interactive mode")
	tokenTracerOpen := flag.Bool("token-tracer-open", defaultTokenTracerOpen(), "open Token Tracer dashboard in the default browser")
	tokenTracerPort := flag.Int("token-tracer-port", defaultTokenTracerPort(), "Token Tracer dashboard port; 0 selects a free port")
	yolo := flag.Bool("yolo", false, "dangerous mode: allow Read to access files outside the workspace")
	dangerously := flag.Bool("dangerously", false, "dangerous mode: allow Read to access files outside the workspace")
	flag.Parse()

	return options{
		prompt:             *prompt,
		sessionID:          *sessionID,
		subagentWorker:     *subagentWorker,
		subagentWorkerPool: *subagentWorkerPool,
		streamMA:           *streamMA,
		tokenTracer:        *tokenTracer,
		tokenTracerOpen:    *tokenTracerOpen,
		tokenTracerPort:    *tokenTracerPort,
		allowOutsideRead:   *yolo || *dangerously,
	}
}

func defaultStreamMAEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PAW_STREAMMA")))
	return value != "0" && value != "false" && value != "off" && value != "no"
}

func defaultTokenTracerEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PAW_TOKEN_TRACER")))
	return value != "0" && value != "false" && value != "off" && value != "no"
}

func defaultTokenTracerOpen() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PAW_TOKEN_TRACER_OPEN")))
	return value == "1" || value == "true" || value == "on" || value == "yes"
}

func defaultTokenTracerPort() int {
	value := strings.TrimSpace(os.Getenv("PAW_TOKEN_TRACER_PORT"))
	if value == "" {
		return 8999
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 0 {
		return 8999
	}
	return port
}
