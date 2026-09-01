// Command webfixture runs a real Paw web workbench server seeded with a
// deterministic workspace for browser end-to-end tests.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	appcore "paw/internal/app"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/session"
	webserver "paw/internal/web"
)

// fixtureRunner accepts every submitted turn and replays a deterministic
// assistant response without contacting a model provider.
type fixtureRunner struct {
	store *session.JSONLStore
}

func (r *fixtureRunner) CurrentSessionID() string { return "fixture-session" }

func (r *fixtureRunner) LoadSession(_ context.Context, sessionID string) (loop.SessionLoadResult, error) {
	return loop.SessionLoadResult{}, nil
}

func (r *fixtureRunner) PrepareSteer(string) (loop.SteerAdmission, bool) {
	return noopSteer{}, true
}

type noopSteer struct{}

func (noopSteer) Commit() {}
func (noopSteer) Abort()  {}

func (r *fixtureRunner) RunTurnWithTiming(ctx context.Context, input, turnID string, startedAt time.Time) (loop.TurnExecution, error) {
	// 使用提交目标会话而非固定会话：TurnService 在提交前已 LoadSession。
	sessionID := r.CurrentSessionID()
	if err := r.store.BeginTurn(ctx, sessionID, turnID, message.Message{Role: message.RoleUser, Content: input}); err != nil {
		return loop.TurnExecution{}, err
	}
	response := message.Message{Role: message.RoleAssistant, Content: "fixture 回复：已收到消息 " + input}
	if err := r.store.AppendAssistant(ctx, sessionID, turnID, response); err != nil {
		return loop.TurnExecution{}, err
	}
	if err := r.store.CompleteTurn(ctx, sessionID, turnID); err != nil {
		return loop.TurnExecution{}, err
	}
	return loop.TurnExecution{Message: response}, nil
}

func main() {
	port := flag.Int("port", 18777, "listen port")
	root := flag.String("workspace", "", "seeded workspace directory")
	flag.Parse()
	if *root == "" {
		log.Fatal("-workspace is required")
	}
	workspace, err := filepath.Abs(*root)
	if err != nil {
		log.Fatal(err)
	}

	store, err := session.NewJSONLStoreForWorkspace(workspace)
	if err != nil {
		log.Fatal(err)
	}
	canonical, err := appcore.CanonicalWorkspace(workspace)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	eventHub, err := appcore.NewEventHub(appcore.EventHubConfig{WorkspaceID: canonical.ID, StreamID: "stream-fixture"})
	if err != nil {
		log.Fatal(err)
	}
	coordinator := appcore.NewWorkspaceCoordinator()
	runtime := &appcore.WorkspaceRuntime{
		Root: canonical.Path, Coordinator: coordinator, EventHub: eventHub,
		SessionService: appcore.NewSessionService(store, coordinator),
	}
	runtime.TurnService = appcore.NewTurnService(&fixtureRunner{store: store}, store, coordinator, eventHub, nil)

	// Seed one session with a completed turn; a reused workspace keeps its data.
	if _, err := store.CreateRoot(ctx, session.CreateRootRequest{SessionID: "fixture-session"}); err == nil {
		if err := store.BeginTurn(ctx, "fixture-session", "fixture-turn-1", message.Message{Role: message.RoleUser, Content: "列出工作区文件"}); err != nil {
			log.Fatal(err)
		}
		if err := store.AppendAssistant(ctx, "fixture-session", "fixture-turn-1", message.Message{Role: message.RoleAssistant, Content: "工作区包含 README.md 与 internal/ 目录。"}); err != nil {
			log.Fatal(err)
		}
		if err := store.CompleteTurn(ctx, "fixture-session", "fixture-turn-1"); err != nil {
			log.Fatal(err)
		}
	}

	recent, err := appcore.NewRecentWorkspaceStore("")
	if err != nil {
		log.Fatal(err)
	}
	if err := recent.Remember(ctx, canonical); err != nil {
		log.Fatal(err)
	}
	supervisor := appcore.NewSupervisor(appcore.SupervisorConfig{
		Capacity: 2, Recent: recent,
		Factory: func(context.Context, appcore.WorkspaceRuntimeOptions) (*appcore.WorkspaceRuntime, error) {
			return runtime, nil
		},
	})
	if _, err := supervisor.Open(ctx, appcore.WorkspaceRuntimeOptions{Root: canonical.Path}); err != nil {
		log.Fatal(err)
	}
	auth := webserver.NewAuthStore(false)
	server, err := webserver.NewServer(webserver.ServerConfig{
		Listen: fmt.Sprintf("127.0.0.1:%d", *port), Supervisor: supervisor, Auth: auth,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := server.Start(); err != nil {
		log.Fatal(err)
	}
	token, err := auth.NewBootstrapToken()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s/#bootstrap=%s\n", server.URL(), token)
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		log.Fatal(err)
	}
}
