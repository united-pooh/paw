// Command dashboardfixture runs a deterministic Token Tracer server for
// browser end-to-end tests: three turns, one failed agent, and exactly
// 2,000 retained events.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"paw/internal/tokentracer"
)

func seed(tracer *tokentracer.Tracer) {
	agents := []struct {
		id       string
		name     string
		provider string
		model    string
		fail     bool
	}{
		{id: "planner", name: "planner", provider: "fixture-provider", model: "gpt-4.1", fail: false},
		{id: "critic", name: "critic", provider: "fixture-provider", model: "gpt-4.1", fail: true},
		{id: "researcher", name: "researcher", provider: "fixture-provider", model: "gpt-4.1-mini", fail: false},
	}
	// The tracer keeps only the most recent 2,000 events, so the deterministic
	// filler events must be emitted before the structural turn events that the
	// timeline projection depends on.
	for i := 0; i < 2000; i++ {
		stageID := fmt.Sprintf("turn-%d", i%3+1)
		if i%3 == 0 {
			tracer.RecordEvent("tool_event", map[string]any{
				"stage_id": stageID,
				"agent_id": agents[i%3].id,
				"tool":     fmt.Sprintf("tool-%d", i%7),
			})
		} else {
			tracer.RecordEvent("cleanup", map[string]any{"stage_id": stageID})
		}
	}
	usages := []tokentracer.Usage{
		{Input: 1200, CacheRead: 3400, CacheCreation: 900, Output: 420},
		{Input: 800, CacheRead: 2100, CacheCreation: 600, Output: 310},
		{Input: 2400, CacheRead: 900, CacheCreation: 1500, Output: 980},
	}
	for i, agent := range agents {
		stageID, _ := tracer.StartTurn(fmt.Sprintf("fixture input %d", i), "conversation")
		for j := 0; j < 3; j++ {
			usage := usages[(i+j)%len(usages)]
			tracer.RecordAPICall(
				stageID, "", agent.id, agent.name, agent.provider, agent.model,
				usage, map[string]any{"step": j, "tool": fmt.Sprintf("tool-%d", j)},
			)
		}
		var err error
		if agent.fail {
			err = errors.New("fixture failure")
		}
		tracer.FinishTurn(stageID, agent.id, err)
	}
}

func main() {
	port := flag.Int("port", 18999, "listen port")
	flag.Parse()
	tracer := tokentracer.New("Token Tracer E2E")
	seed(tracer)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := tokentracer.NewServer(tracer, tokentracer.ServerConfig{Host: "127.0.0.1", Port: *port})
	if err := server.Start(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println(server.URL())
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		log.Fatal(err)
	}
}
