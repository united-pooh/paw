# Paw MCP Client for CodeGraph Implementation Plan

> **For Codex workers:** Implement task-by-task. Use update_plan to track progress, keep one step in progress at a time, edit files with apply_patch, and run the exact verification commands listed below. Steps use checkbox syntax for tracking.

**Goal:** Add a generic stdio MCP client that launches CodeGraph from the main Agent, exposes MCP tools/resources/prompts through the existing tool loop, and brokers those calls to subagents through a bidirectional worker protocol.

**Architecture:** Add internal/mcp for TOML configuration, stdio JSON-RPC sessions, capability snapshots, real MCP adapters, and the parent broker interface. Add internal/tool/mcp for local and parent-proxy Tool implementations. Extend the subagent worker channel with request-ID envelopes so the parent owns the real MCP processes while child workers proxy calls.

**Tech Stack:** Go 1.25, newline-delimited JSON-RPC 2.0, github.com/BurntSushi/toml, existing model.ToolDefinition and tool.Tool interfaces, Bubble Tea command injection, and Go test fake stdio servers.

---

## Current repository constraints

- The real executable entrypoint is cmd/agent/main.go.
- Runner already sends tool.Registry.Definitions to the model and executes tool.Tool calls; it must not gain MCP-specific branching.
- tool.Registry is currently a plain map with no mutex, replacement, or collision error API.
- cmd/agent/buildRunnerWithSubagentContext constructs the main Runner and subagent.Manager.
- The current external subagent launcher writes one JSON WorkerRequest to stdin and reads one final WorkerResult from stdout. MCP brokering requires a bidirectional framed channel.
- Existing uncommitted Bubble UI edits and unrelated untracked files must not be staged or modified.
- The accepted design is docs/superpowers/specs/2026-07-29-mcp-codegraph-design.md.

## Task 1: Add MCP configuration and shared capability types

Files:

- Create internal/mcp/config.go
- Create internal/mcp/config_test.go
- Create internal/mcp/types.go
- Create internal/mcp/types_test.go
- Modify go.mod and go.sum to add github.com/BurntSushi/toml

Steps:

- [ ] Write tests for missing-config creation, Codex-style mcp_servers parsing, cwd resolution, env overlay, defaults, disabled entries, and invalid enabled servers.
- [ ] Run go test ./internal/mcp -run TestLoadConfig -count=1 and verify it fails because internal/mcp is absent.
- [ ] Add github.com/BurntSushi/toml and implement LoadConfig(homeDir, workspaceRoot) with mode 0700 for ~/.paw and mode 0600 for the absent mcp.toml.
- [ ] Validate server names and enabled commands; resolve cwd against the workspace root; preserve disabled entries for status.
- [ ] Define CapabilityKind, ToolSpec, Snapshot, and Broker in internal/mcp/types.go. ToolSpec must retain both the model-facing qualified name and the original MCP server/name pair.
- [ ] Run go test ./internal/mcp -count=1 and verify configuration and type tests pass.

The shared types must expose these method shapes:

    type Broker interface {
        Snapshot() Snapshot
        Call(context.Context, string, json.RawMessage) (string, error)
    }

    type ServerStatus struct {
        Name, Command, WorkDir, State, LastError string
        PID, Tools, Resources, Templates, Prompts int
    }

## Task 2: Implement newline-delimited stdio JSON-RPC sessions

Files:

- Create internal/mcp/jsonrpc.go and jsonrpc_test.go
- Create internal/mcp/session.go and session_test.go

Steps:

- [ ] Add a fake stdio peer test that returns concurrent responses out of order, sends notifications, and exercises malformed output and context cancellation.
- [ ] Run go test ./internal/mcp -run TestSession -count=1 and verify failure before implementation.
- [ ] Implement JSON-RPC 2.0 request/response/notification decoding with one stdout reader goroutine, a request-ID pending map, a notification channel, and serialized writes.
- [ ] Implement process-backed Session with exec.CommandContext, stdin/stdout pipes, a bounded 32 KiB stderr buffer, 10-second startup handling, 120-second call handling, and two-second graceful close.
- [ ] Run go test ./internal/mcp -run 'TestSession|TestRPC' -count=1 and verify all session tests pass.

Session must expose:

    type RPCSession interface {
        Call(context.Context, string, any, any) error
        Notifications() <-chan Notification
        Close(context.Context) error
    }

## Task 3: Implement MCP Manager, discovery, calls, and adapters

Files:

- Create internal/mcp/manager.go, manager_test.go
- Create internal/mcp/discovery.go, discovery_test.go
- Create internal/tool/mcp/mcp.go, mcp_test.go
- Create internal/mcp/testserver/main.go as the executable fake server used by smoke tests

Steps:

- [ ] Add a fake MCP server helper implementing initialize, initialized, paginated tools/list, resources/list, resource templates/list, prompts/list, tools/call, resources/read, and prompts/get.
- [ ] Test discovery of a CodeGraph-shaped tool plus generic resource and prompt virtual tools.
- [ ] Implement Manager.Start, Snapshot, Call, Status, and Close. Start all enabled servers in parallel, negotiate, page through all lists, and build a qualified-name index.
- [ ] Normalize model-facing names by replacing non-ASCII alphanumeric, underscore, and hyphen runes with underscore. Reject empty names, invalid schemas, duplicate names, and pre-existing collisions.
- [ ] Implement tools/list_changed, resources/list_changed, and prompts/list_changed refreshes with a monotonically increasing snapshot version.
- [ ] Implement internal/tool/mcp.NewTools(Snapshot, mcp.Broker) []tool.Tool and the Tool type with Name, Description, InputSchema, and Run. The real Manager is the local adapter Caller, and Task 6 wires the same adapter shape to a parent proxy.
- [ ] Render one text block directly and serialize multiple/non-text MCP content as JSON. Convert MCP isError and transport failures into Go errors. Do not implement ConcurrencySafeTool initially; Runner serializes calls within one turn while the Manager still correlates concurrent calls from multiple subagents.
- [ ] Run go test ./internal/mcp ./internal/tool/mcp -count=1.

Manager must expose:

    func Start(context.Context, Config, string) (*Manager, error)
    func (m *Manager) Snapshot() Snapshot
    func (m *Manager) Call(context.Context, string, json.RawMessage) (string, error)
    func (m *Manager) Status() []ServerStatus
    func (m *Manager) Close(context.Context) error

## Task 4: Make Registry concurrency-safe and namespace-aware

Files:

- Modify internal/tool/register.go
- Create internal/tool/register_test.go

Steps:

- [ ] Add tests for RegisterChecked, namespace replacement, built-in collision rejection, stale-name removal, and concurrent Definitions/Get during replacement.
- [ ] Run go test ./internal/tool -run TestRegistry -count=1 and verify the new tests fail.
- [ ] Add sync.RWMutex protection to all registry map access while preserving existing Register behavior for built-ins.
- [ ] Add RegisterChecked and ReplaceNamespace. Validate every incoming name and every non-MCP collision before mutating the map; replace only names under prefix plus double underscore.
- [ ] Run go test ./internal/tool -run TestRegistry -count=1 and verify the tests pass.

## Task 5: Integrate the real Manager into main Agent and add /mcp

Files:

- Modify cmd/agent/main.go
- Modify internal/ui/bubble/bubble.go, types.go, app.go, command_registry.go, and bubble_test.go
- Create cmd/agent/mcp_integration_test.go

Steps:

- [ ] Add a temporary fake MCP command test that verifies buildRunner starts one Manager, registers a qualified definition, creates the absent config, and closes the Manager in single-turn/interactive ownership paths.
- [ ] Add an optional broker parameter to the internal buildRunner helper. Main mode starts the real Manager and passes it to the main Registry and subagent.Manager; worker mode supplies a proxy and does not start a real Manager. Call internal/tool/mcp.NewTools for the current snapshot and apply each server's tools through Registry.ReplaceNamespace using names such as codegraph, not a shared mcp prefix.
- [ ] Keep existing buildRunner wrappers where tests depend on them, and return/own a closeable Manager without changing Runner's public interface.
- [ ] Add MCPStatusController and UI.SetMCPController. Store the controller in appModel after newModel construction so existing newModel call sites do not need a new parameter.
- [ ] Register /mcp with AllowWhileRunning true in command_registry.go. Format config path, server state/PID, command/cwd, capability counts, active proxy calls, and sanitized latest errors; nil controller reports MCP not configured.
- [ ] Test command lookup, help text, completion, nil-controller output, status formatting, and secret-free output.
- [ ] Run go test ./cmd/agent ./internal/ui/bubble -count=1.

## Task 6: Implement parent-owned subagent broker protocol

Files:

- Modify internal/subagent/worker.go and manager.go
- Create internal/subagent/broker.go and broker_test.go
- Modify internal/subagent/manager_test.go
- Modify cmd/agent/main.go

Steps:

- [ ] Define WorkerEnvelope, ProxyCall, and ProxyResult. Add MCPSnapshot *mcp.Snapshot to WorkerRequest. worker.start carries WorkerRequest plus the current snapshot; child emits mcp.call; parent emits mcp.result; parent emits mcp.snapshot; child emits worker.result; either side can emit worker.cancel.
- [ ] Add a fake child test where mcp.call arrives before worker.result and verify matching request IDs, final WorkerResult decoding, unknown-name rejection, snapshot replacement, and cancellation.
- [ ] Add optional BrokerProcess to the existing Process interface with MCPCalls, RespondMCP, and SendMCPSnapshot, keeping old blocking test doubles valid.
- [ ] Replace external execProcess byte buffers with stdin/stdout pipes, a write mutex, an envelope reader, an MCP call channel, and a final result channel. ProcessLauncher sends worker.start and keeps the channel open until worker.result.
- [ ] Add mcp.Broker to subagent.Config and Manager. When a process implements BrokerProcess, forward every allowed call to the parent broker and return mcp.result; fail pending calls on disconnect.
- [ ] In worker mode, decode worker.start, construct a proxy broker over the worker channel, pass it into buildRunnerWithSubagentContext, and register parent-proxy MCP tools without starting MCP servers.
- [ ] Propagate the broker into nested subagent Managers. In-process launchers call the same Broker directly; external workers use the framed channel.
- [ ] Preserve existing WorkerResult fields and task tracking. Ensure Stop and context cancellation close the channel and leave no worker process.
- [ ] Run go test ./internal/subagent ./cmd/agent -count=1.

The proxy process interface must remain request-ID based:

    type BrokerProcess interface {
        Process
        MCPCalls() <-chan ProxyCall
        RespondMCP(ProxyResult) error
        SendMCPSnapshot(mcp.Snapshot) error
    }

## Task 7: Full verification and CodeGraph smoke test

Files:

- Modify only files required by failing verification tests.
- Do not stage unrelated existing UI edits or unrelated untracked files.

Steps:

- [ ] Run go test ./... -count=1, NO_COLOR=1 go test ./internal/ui/bubble -count=1, and git diff --check.
- [ ] Run a fake-server interactive smoke test with go run ./cmd/agent/main.go, /mcp, one qualified tool call, and one ordinary built-in tool call.
- [ ] When codegraph is installed, use command = "codegraph" and args = ["serve", "--mcp"], launch go run ./cmd/agent/main.go, verify /mcp, and exercise one codegraph__... call.
- [ ] Verify the main process owns one CodeGraph process with multiple subagents and that parent proxy calls complete with correct results.
- [ ] Inspect git diff --stat and git status --short. Preserve the pre-existing Bubble UI changes and unrelated files.
