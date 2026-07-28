# GoCode MCP Client for CodeGraph

Date: 2026-07-29
Status: Revised design pending written-spec review

## 1. Objective

Add MCP client support to GoCode so it can launch and use local stdio MCP servers, with CodeGraph as the first target. The feature must reuse the existing tool.Tool, tool.Registry, Runner tool loop, model tool definitions, and UI tool events.

The implementation is a generic multi-server client rather than a CodeGraph-only special case. It follows the Codex-style server configuration shape, but uses an independent GoCode configuration file:

~~~text
~/.ccagent/mcp.toml
~~~

The main Agent process owns the real MCP server sessions. Subagents receive the current MCP capability snapshot and route MCP calls through a parent-process broker. This keeps one CodeGraph process per main Agent, avoids duplicate indexes, and provides a stable path for later subagent process/session reuse.

## 2. Confirmed decisions

- GoCode is an MCP client, not an MCP server.
- The first transport is local stdio only.
- The configuration supports multiple MCP servers.
- Configuration is global and independent from Codex: ~/.ccagent/mcp.toml.
- A missing configuration file is created as an empty file and does not block normal startup.
- Configured servers start during process initialization and complete MCP initialization before the Runner is usable.
- A configured server that cannot initialize causes that GoCode process to fail startup.
- Server and tool names are namespaced with <server>__<name>.
- MCP tools, resources, resource templates, and prompts are all exposed to the model through virtual GoCode tools.
- /mcp reports server state and discovered capability counts.
- There is no automatic MCP server restart in the first implementation.
- The main Agent owns the MCP Manager; subagents use proxy tools backed by the parent broker.
- The parent/subagent worker channel is bidirectional and request-ID based so MCP calls can be multiplexed with the final worker result.

## 3. Scope and non-goals

### In scope

- JSON-RPC 2.0 over newline-delimited stdio.
- MCP lifecycle initialization and capability negotiation.
- tools/list and tools/call.
- resources/list, resources/templates/list, and resources/read.
- prompts/list and prompts/get.
- Pagination for all list methods.
- Capability change notifications, parent snapshot refresh, and registry refresh.
- Parent-process broker protocol for subagent MCP proxy calls.
- MCP server stderr capture for diagnostics.
- Per-server lifecycle, status, timeout, and process cleanup.
- Adapter integration with existing model providers and UI events.

### Not in scope

- GoCode acting as an MCP server.
- HTTP, Streamable HTTP, or legacy HTTP/SSE transports.
- Independent MCP server sessions inside subagent processes; subagents use the parent broker instead.
- Automatic server restart or reconnect.
- MCP client features such as sampling, roots, or elicitation.
- Native multimodal model messages. Non-text MCP content is preserved in the textual tool-result representation described below.

The stdio framing follows the MCP transport rules: the child process reads JSON-RPC messages from stdin, writes JSON-RPC messages to stdout, uses newline-delimited messages without embedded newlines, and may write diagnostics to stderr. See the MCP stdio transport specification: https://modelcontextprotocol.io/specification/2025-06-18/basic/transports.

## 4. Configuration

The file uses the Codex-style mcp_servers table:

~~~toml
[mcp_servers.codegraph]
command = "codegraph"
args = ["serve", "--mcp"]
cwd = "."
enabled = true

[mcp_servers.codegraph.env]
CODEGRAPH_MCP_TOOLS = "explore,context"
~~~

### Configuration rules

- The path is resolved from os.UserHomeDir() as <home>/.ccagent/mcp.toml.
- A missing parent directory is created with mode 0700.
- A missing file is created empty with mode 0600.
- Failure to create or read the configuration is reported as a startup error.
- command is required for an enabled server.
- args defaults to an empty list.
- cwd defaults to the current GoCode workspace root.
- A relative cwd is resolved against the workspace root, not the home directory.
- enabled defaults to true.
- The child inherits the parent environment, then the configured env table overrides matching variables.
- The command is executed directly with exec.CommandContext; it is never passed through a shell.
- Server configuration names must be unique and contain only ASCII letters, digits, underscore, or hyphen.
- Environment values are never included in /mcp output or error messages.

The initial implementation uses internal timeout defaults instead of adding extra configuration fields:

- startup and initialization: 10 seconds;
- individual MCP request: 120 seconds;
- graceful process shutdown: 2 seconds before forced termination.

## 5. Architecture

### internal/mcp

This package owns the MCP protocol and process lifecycle:

- ConfigLoader: loads, validates, and creates ~/.ccagent/mcp.toml.
- Manager: starts enabled servers, exposes aggregate status, and closes sessions.
- Session: owns one child process, stdin/stdout/stderr, request IDs, pending responses, and negotiated capabilities.
- JSON-RPC dispatcher: correlates responses with requests and delivers notifications.
- capability snapshots: store discovered tools, resources, templates, prompts, and server state.

The dispatcher uses monotonically increasing request IDs per session and a pending-request map protected by a mutex. One reader goroutine owns stdout decoding. Tool calls from multiple Runner goroutines may be in flight concurrently and are matched by ID.

The Manager exposes a parent broker interface with two operations:

- return the current model-facing MCP tool definitions and capability snapshot;
- invoke a qualified MCP tool name with JSON input under a caller context.

The broker validates that the qualified name exists in the advertised snapshot before forwarding the call to the owning MCP session. Subagents cannot send arbitrary MCP methods or server names to the parent.

### internal/tool/mcp

This package adapts MCP capability snapshots to the existing tool.Tool interface:

- MCP tools become normal tool.Tool implementations.
- Resource and prompt operations become virtual tool.Tool implementations.
- The adapter keeps the original MCP name and routes calls through the owning session.
- MCP input schemas are passed through as InputSchema after JSON validation.

### Existing integration points

- cmd/agent/main.go creates the MCP Manager while building the Runner.
- Built-in tools are registered first; the main Agent registers local MCP adapters, while subagent workers register parent-proxy adapters from the broker snapshot.
- The main process owns the Manager close operation for interactive and single-turn modes.
- The subagent Manager receives a broker client; it does not start or close MCP server processes.
- internal/loop/runner.go continues to use the existing registry, model tool definitions, tool events, and tool-result messages.
- internal/ui/bubble receives MCP calls through the existing OnToolCall and OnToolResult events, so no MCP-specific rendering path is required.

The existing Runner tool loop does not become MCP-aware. MCP remains an implementation detail of registered tools.

## 6. MCP lifecycle and data flow

At main Agent startup, for each enabled server:

1. Resolve command, arguments, working directory, and environment.
2. Start the child process.
3. Connect stdin and stdout to the JSON-RPC session.
4. Capture stderr in a bounded 32 KiB diagnostic buffer.
5. Send initialize with the GoCode client identity and supported protocol revision.
6. Validate the server response and negotiated capabilities.
7. Send notifications/initialized.
8. Discover all paginated tools, resources, resource templates, and prompts.
9. Build the server's namespaced tool set.
10. Atomically add the tool set to the GoCode registry.

All enabled servers start in parallel. If any server fails before capability discovery completes, the Manager closes all servers it started and returns an error containing the server name, command, failure phase, and bounded stderr tail.

After startup:

- tools/list_changed refreshes the server's tools and atomically replaces only that server namespace.
- resources/list_changed refreshes resources and templates.
- prompts/list_changed refreshes prompts.
- A server process exit marks that server unavailable. Existing wrappers remain present so a subsequent model call receives a clear MCP-unavailable tool error.
- No automatic restart is attempted.

On main process shutdown, the Manager cancels sessions, closes stdin, waits up to two seconds, and force-terminates remaining children.

### Parent/subagent broker protocol

The current subagent worker channel is one-shot stdin plus one final stdout result. To support parent-owned MCP and future long-lived subagent reuse, the worker channel becomes a bidirectional newline-delimited JSON envelope protocol:

- parent sends worker.start with task metadata and the current MCP capability snapshot;
- child sends mcp.call with a request ID, qualified tool name, and JSON input;
- parent sends mcp.result with the same request ID, rendered content, and error state;
- parent sends mcp.snapshot when MCP capability lists change;
- child sends worker.result when the task completes;
- either side can send worker.cancel to terminate pending work.

The parent process reader multiplexes mcp.call and worker.result messages from each child process. It invokes the real MCP Manager for mcp.call and writes the matching mcp.result back to that child. Calls from different subagents and calls within one subagent are correlated by process identity plus request ID.

The child constructs proxy tools from the snapshot included in worker.start. Proxy tools use the same model-facing names and schemas as the main Agent, but their Run method sends mcp.call to the parent instead of touching a local MCP session. A mcp.snapshot update atomically replaces only the MCP proxy namespace before the next model step.

The in-process subagent launcher uses the same broker interface without serializing through pipes. The external process launcher uses the framed protocol above. The worker protocol continues to carry the final WorkerResult fields required by existing task tracking.

## 7. MCP-to-GoCode tool mapping

### MCP tools

For every discovered MCP tool named name on server server, register:

~~~text
<server>__<name>
~~~

The wrapper uses the MCP tool description and input schema. It calls tools/call with the original MCP name and returns the MCP result.

The main Agent wrapper calls the owning MCP session directly. A subagent wrapper keeps the same name, description, and schema but sends the qualified name and input to the parent broker. The model cannot distinguish the two paths.

### Resources

Register the following generic virtual tools per server:

| Virtual tool | Input | MCP method |
|---|---|---|
| <server>__list_resources | {} | resources/list |
| <server>__list_resource_templates | {} | resources/templates/list |
| <server>__read_resource | {"uri": "..."} | resources/read |

The model can list resources or templates, then request a specific URI through read_resource.

### Prompts

Register:

| Virtual tool | Input | MCP method |
|---|---|---|
| <server>__list_prompts | {} | prompts/list |
| <server>__get_prompt | {"name": "...", "arguments": {}} | prompts/get |

Prompt messages are rendered into the returned textual representation; they are not injected into the system prompt automatically.

### Name normalization and collisions

- Server configuration names are validated before launch.
- MCP capability names are retained internally exactly as supplied by the server.
- For the model-facing name, every rune outside [A-Za-z0-9_-] is replaced with underscore.
- A normalized empty name is invalid.
- If two capabilities produce the same final name, startup or refresh fails for that server.
- A final MCP name may not overwrite a built-in tool or another server's tool.
- The original MCP name is always used when sending tools/call, resources/read, or prompts/get.
- A subagent may invoke only names present in its latest parent-provided snapshot.

### Result rendering

- One text content block is returned as its text.
- Multiple content blocks, embedded resources, binary blobs, or structured content are returned as a JSON object containing the original content metadata and data.
- MCP isError=true is converted into a GoCode tool error, preserving the rendered content.
- Transport, timeout, protocol, and process errors are returned as Go errors, allowing the existing Runner to set ToolResult.IsError.

## 8. Registry refresh support

The current registry is sufficient for initial registration but needs a focused extension for MCP notifications:

- protect registry reads and writes with a mutex;
- add an atomic namespace replacement operation for MCP tools;
- remove stale tools belonging to one MCP server before adding its refreshed set;
- reject collisions with non-MCP tools before applying a replacement;
- keep built-in registrations unchanged.

This is a targeted registry capability, not a general tool-system rewrite. Runner behavior and the tool.Tool interface remain unchanged.

## 9. /mcp command

Add /mcp to the existing Bubble command registry. With no arguments it displays:

- MCP configuration path;
- each configured server and enabled/disabled state;
- process PID and running/unavailable state;
- command and working directory;
- discovered counts for tools, resources, resource templates, and prompts;
- active subagent proxy sessions and in-flight brokered calls;
- the latest non-secret error, if any.

The first version does not add manual reconnect or refresh commands. Capability change notifications refresh state automatically.

In headless and single-turn modes, startup errors continue to be returned through the existing command error path. In interactive mode, /mcp provides the runtime status after startup.

## 10. Error and security behavior

- Invalid TOML, missing enabled-server command, unsupported initialization response, malformed JSON-RPC, invalid tool schema, and startup timeout fail the current GoCode process before the Runner starts.
- A tool-call timeout or MCP execution error affects only that tool call and is returned to the model as an error result.
- A brokered subagent call is rejected if its name is absent from the current snapshot, and parent/child disconnects fail all pending broker calls.
- Unexpected stdout that is not a valid JSON-RPC message is a protocol failure; it is never forwarded to the model.
- stderr is diagnostic data only and is bounded before inclusion in errors or /mcp.
- Environment variables are inherited and overlaid but never logged.
- Commands are executed directly, without shell interpolation.
- MCP child processes and subagent worker processes are always closed by their owning parent process.
- The implementation does not infer permissions from tool names; the configured MCP server remains responsible for its own capability policy.

## 11. Testing strategy

### Configuration tests

- missing ~/.ccagent/mcp.toml creates an empty file;
- valid Codex-style tables load correctly;
- enabled, default cwd, relative cwd, args, and environment overlay behave as specified;
- invalid TOML and invalid server names fail clearly;
- environment values do not appear in status or error output.

### Protocol and process tests

Use a Go fake stdio MCP server to verify:

- newline-delimited JSON-RPC framing;
- initialize and initialized notification;
- request ID correlation under concurrent calls;
- pagination for every list method;
- capability change notifications;
- stderr capture;
- startup timeout, call timeout, malformed stdout, and child exit;
- graceful close and forced cleanup.

### Broker protocol tests

- worker.start carries a complete MCP snapshot;
- concurrent mcp.call requests receive matching mcp.result responses;
- worker.result can be multiplexed with pending MCP calls;
- parent rejects unknown qualified names;
- mcp.snapshot updates refresh a child proxy namespace;
- parent/child cancellation fails pending calls and leaves no worker process behind.

### Adapter tests

- namespaced MCP tool definitions;
- JSON Schema pass-through;
- resource and prompt virtual-tool input schemas;
- original-name routing;
- single text, multi-block, resource, blob, and structured results;
- isError mapping;
- collision detection and namespace replacement.

### Application tests

- main Agent starts MCP and registers tools;
- one-shot mode closes MCP after the turn;
- subagent-worker mode receives a snapshot and registers parent-proxy MCP tools without starting an MCP server;
- a future long-lived worker can execute multiple turns through the same parent broker without reinitializing MCP;
- the main process owns one CodeGraph server even when multiple subagents are active;
- /mcp reports configured and runtime states;
- built-in tools and existing Runner tests remain unchanged.

### Acceptance commands

~~~bash
go test ./... -count=1
NO_COLOR=1 go test ./internal/ui/bubble -count=1
git diff --check
~~~

When the local CodeGraph executable is available, perform a manual smoke test with:

~~~toml
[mcp_servers.codegraph]
command = "codegraph"
args = ["serve", "--mcp"]
~~~

Then launch the real Agent entrypoint, run /mcp, and verify that a model-generated codegraph__... call reaches CodeGraph and appears through the existing tool event path.

## 12. Expected implementation shape

The implementation plan should create the MCP core and main-process broker in focused packages, add local and parent-proxy adapters, extend the subagent worker channel with framed bidirectional messages, add only the registry namespace functionality required for live refresh, wire lifecycle ownership into main and worker modes, add /mcp, and add fake-server plus broker tests before the real CodeGraph smoke test.

No changes to the existing uncommitted Bubble UI files are part of this design.
