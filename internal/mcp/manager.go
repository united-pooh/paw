package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	startupTimeout = 10 * time.Second
	callTimeout    = 120 * time.Second
	closeTimeout   = 2 * time.Second

	protocolVersion = "2025-06-18"
	clientName      = "paw"
	clientVersion   = "0.1.0"
)

type Manager struct {
	config Config

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.RWMutex
	servers   map[string]*managedServer
	tools     map[string]ToolSpec
	snapshot  Snapshot
	subs      map[chan Snapshot]struct{}
	closeOnce sync.Once
	closeErr  error
}

type managedServer struct {
	config       ServerConfig
	session      *processSession
	capabilities map[string]any
	tools        []ToolSpec
	status       ServerStatus
}

// Start launches every enabled configured MCP server, initializes it, and
// discovers all initial capabilities before returning.
func Start(ctx context.Context, config Config) (*Manager, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	managerCtx, cancel := context.WithCancel(ctx)
	m := &Manager{
		config:  config,
		ctx:     managerCtx,
		cancel:  cancel,
		servers: make(map[string]*managedServer, len(config.Servers)),
		tools:   make(map[string]ToolSpec),
		subs:    make(map[chan Snapshot]struct{}),
	}

	names := make([]string, 0, len(config.Servers))
	for name, serverConfig := range config.Servers {
		state := "disabled"
		if serverConfig.Enabled {
			state = "starting"
		}
		m.servers[name] = &managedServer{
			config: serverConfig,
			status: ServerStatus{
				Name:    name,
				Command: serverConfig.Command,
				WorkDir: serverConfig.WorkDir,
				State:   state,
			},
		}
		if serverConfig.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	type startupResult struct {
		name         string
		session      *processSession
		capabilities map[string]any
		tools        []ToolSpec
		counts       capabilityCounts
		err          error
	}
	results := make(chan startupResult, len(names))
	for _, name := range names {
		server := m.servers[name]
		go func() {
			result := startupResult{name: name}
			startupCtx, cancelStartup := context.WithTimeout(managerCtx, startupTimeout)
			defer cancelStartup()
			session, err := startSession(context.Background(), server.config)
			if err != nil {
				result.err = startupError(server.config, "start", err, "")
				results <- result
				return
			}
			result.session = session
			result.tools, result.counts, result.capabilities, result.err = initializeAndDiscover(startupCtx, server.config, session)
			if result.err != nil {
				result.err = startupError(server.config, "initialize or discover", result.err, session.StderrTail())
			}
			results <- result
		}()
	}

	for range names {
		result := <-results
		server := m.servers[result.name]
		if result.err != nil {
			server.status.State = "unavailable"
			server.status.LastError = truncateDiagnostic(result.err.Error())
			if result.session != nil {
				server.status.PID = result.session.PID()
				closeCtx, cancelClose := context.WithTimeout(context.Background(), closeTimeout)
				_ = result.session.Close(closeCtx)
				cancelClose()
			}
			continue
		}
		server.session = result.session
		server.capabilities = cloneCapabilities(result.capabilities)
		server.tools = result.tools
		server.status.State = "running"
		server.status.PID = result.session.PID()
		server.status.Tools = result.counts.tools
		server.status.Resources = result.counts.resources
		server.status.Templates = result.counts.templates
		server.status.Prompts = result.counts.prompts
		server.status.BlockedTools = result.counts.blockedTools
	}
	for _, name := range names {
		server := m.servers[name]
		if server.status.State != "running" {
			continue
		}
		if err := m.replaceServerTools(name, server.tools); err != nil {
			_ = m.Close(context.Background())
			return nil, err
		}
	}
	for _, name := range names {
		server := m.servers[name]
		if server.status.State == "running" {
			m.watchServer(name, server)
		}
	}
	go func() {
		<-managerCtx.Done()
		_ = m.Close(context.Background())
	}()
	m.publishSnapshot()
	return m, nil
}

func initializeAndDiscover(ctx context.Context, config ServerConfig, session *processSession) ([]ToolSpec, capabilityCounts, map[string]any, error) {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    clientName,
			"version": clientVersion,
		},
	}
	var initialized initializeResult
	if err := session.Call(ctx, "initialize", params, &initialized); err != nil {
		return nil, capabilityCounts{}, nil, err
	}
	if strings.TrimSpace(initialized.ProtocolVersion) == "" {
		return nil, capabilityCounts{}, nil, errors.New("initialize response has no protocolVersion")
	}
	if err := session.Notify(ctx, "notifications/initialized", nil); err != nil {
		return nil, capabilityCounts{}, nil, err
	}
	tools, counts, err := discoverCapabilities(ctx, config.Name, session, initialized.Capabilities)
	return tools, counts, cloneCapabilities(initialized.Capabilities), err
}

type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      map[string]any `json:"serverInfo"`
}

type capabilityCounts struct {
	tools, resources, templates, prompts, blockedTools int
}

type pagedTools struct {
	Tools      []mcpToolInfo `json:"tools"`
	NextCursor string        `json:"nextCursor"`
}

type mcpToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type pagedResources struct {
	Resources  []json.RawMessage `json:"resources"`
	NextCursor string            `json:"nextCursor"`
}

type pagedTemplates struct {
	Templates  []json.RawMessage `json:"resourceTemplates"`
	NextCursor string            `json:"nextCursor"`
}

type pagedPrompts struct {
	Prompts    []json.RawMessage `json:"prompts"`
	NextCursor string            `json:"nextCursor"`
}

func discoverCapabilities(ctx context.Context, serverName string, session RPCSession, capabilities map[string]any) ([]ToolSpec, capabilityCounts, error) {
	tools := make([]ToolSpec, 0)
	var listedTools []mcpToolInfo
	var err error
	if capabilityEnabled(capabilities, "tools") {
		listedTools, err = listTools(ctx, session)
		if err != nil {
			return nil, capabilityCounts{}, err
		}
	}
	for _, item := range listedTools {
		if isSensitiveMCPToolName(item.Name) {
			continue
		}
		name, err := namespacedCapabilityName(serverName, item.Name)
		if err != nil {
			return nil, capabilityCounts{}, err
		}
		schema := item.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`)
		}
		if !json.Valid(schema) {
			return nil, capabilityCounts{}, fmt.Errorf("MCP tool %q has invalid input schema", item.Name)
		}
		tools = append(tools, ToolSpec{
			Name:        name,
			Description: item.Description,
			InputSchema: append(json.RawMessage(nil), schema...),
			Server:      serverName,
			MCPName:     item.Name,
			Kind:        KindTool,
		})
	}
	var resources []json.RawMessage
	if capabilityEnabled(capabilities, "resources") {
		resources, err = listResources(ctx, session)
		if err != nil {
			return nil, capabilityCounts{}, err
		}
	}
	var templates []json.RawMessage
	if capabilityEnabled(capabilities, "resources") {
		templates, err = listTemplates(ctx, session)
		if err != nil {
			return nil, capabilityCounts{}, err
		}
	}
	var prompts []json.RawMessage
	if capabilityEnabled(capabilities, "prompts") {
		prompts, err = listPrompts(ctx, session)
		if err != nil {
			return nil, capabilityCounts{}, err
		}
	}
	tools = append(tools, virtualCapabilities(serverName)...)
	if err := validateUniqueTools(tools); err != nil {
		return nil, capabilityCounts{}, err
	}
	blockedTools := 0
	for _, item := range listedTools {
		if isSensitiveMCPToolName(item.Name) {
			blockedTools++
		}
	}
	return tools, capabilityCounts{
		tools:        len(listedTools) - blockedTools,
		resources:    len(resources),
		templates:    len(templates),
		prompts:      len(prompts),
		blockedTools: blockedTools,
	}, nil
}

func isSensitiveMCPToolName(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "show_api_key")
}

func capabilityEnabled(capabilities map[string]any, name string) bool {
	if len(capabilities) == 0 {
		return true
	}
	_, ok := capabilities[name]
	return ok
}

func cloneCapabilities(capabilities map[string]any) map[string]any {
	if len(capabilities) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(capabilities))
	for name, value := range capabilities {
		cloned[name] = value
	}
	return cloned
}

func listTools(ctx context.Context, session RPCSession) ([]mcpToolInfo, error) {
	var all []mcpToolInfo
	cursor := ""
	for {
		var page pagedTools
		if err := callPage(ctx, session, "tools/list", cursor, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Tools...)
		if page.NextCursor == "" {
			return all, nil
		}
		cursor = page.NextCursor
	}
}

func listResources(ctx context.Context, session RPCSession) ([]json.RawMessage, error) {
	return listRawPages(ctx, session, "resources/list", "resources")
}

func listTemplates(ctx context.Context, session RPCSession) ([]json.RawMessage, error) {
	return listRawPages(ctx, session, "resources/templates/list", "resourceTemplates")
}

func listPrompts(ctx context.Context, session RPCSession) ([]json.RawMessage, error) {
	return listRawPages(ctx, session, "prompts/list", "prompts")
}

func listRawPages(ctx context.Context, session RPCSession, method, field string) ([]json.RawMessage, error) {
	all := make([]json.RawMessage, 0)
	cursor := ""
	for {
		var raw map[string]json.RawMessage
		if err := callPage(ctx, session, method, cursor, &raw); err != nil {
			return nil, err
		}
		var items []json.RawMessage
		if data := raw[field]; len(data) > 0 {
			if err := json.Unmarshal(data, &items); err != nil {
				return nil, fmt.Errorf("decode %s response: %w", method, err)
			}
		}
		all = append(all, items...)
		var next string
		if data := raw["nextCursor"]; len(data) > 0 {
			if err := json.Unmarshal(data, &next); err != nil {
				return nil, fmt.Errorf("decode %s cursor: %w", method, err)
			}
		}
		if next == "" {
			return all, nil
		}
		cursor = next
	}
}

func callPage(ctx context.Context, session RPCSession, method, cursor string, result any) error {
	params := map[string]string{}
	if cursor != "" {
		params["cursor"] = cursor
	}
	return session.Call(ctx, method, params, result)
}

func virtualCapabilities(serverName string) []ToolSpec {
	return []ToolSpec{
		virtualTool(serverName, "list_resources", "List resources exposed by this MCP server.", KindListResources, "resources/list", `{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		virtualTool(serverName, "list_resource_templates", "List resource templates exposed by this MCP server.", KindListTemplates, "resources/templates/list", `{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		virtualTool(serverName, "read_resource", "Read a resource URI exposed by this MCP server.", KindReadResource, "resources/read", `{"type":"object","properties":{"uri":{"type":"string"}},"required":["uri"]}`),
		virtualTool(serverName, "list_prompts", "List prompts exposed by this MCP server.", KindListPrompts, "prompts/list", `{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		virtualTool(serverName, "get_prompt", "Get a rendered prompt from this MCP server.", KindGetPrompt, "prompts/get", `{"type":"object","properties":{"name":{"type":"string"},"arguments":{"type":"object"}},"required":["name"]}`),
	}
}

func virtualTool(serverName, suffix, description string, kind CapabilityKind, method, schema string) ToolSpec {
	name, _ := namespacedCapabilityName(serverName, suffix)
	return ToolSpec{
		Name:        name,
		Description: description,
		InputSchema: json.RawMessage(schema),
		Server:      serverName,
		MCPName:     method,
		Kind:        kind,
	}
}

func namespacedCapabilityName(serverName, capabilityName string) (string, error) {
	serverName = strings.TrimSpace(serverName)
	capabilityName = strings.TrimSpace(capabilityName)
	if serverName == "" || capabilityName == "" {
		return "", errors.New("MCP capability name is empty")
	}
	return serverName + "__" + normalizeName(capabilityName), nil
}

func normalizeName(name string) string {
	var builder strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

func validateUniqueTools(tools []ToolSpec) error {
	seen := make(map[string]struct{}, len(tools))
	for _, item := range tools {
		if item.Name == "" {
			return errors.New("MCP capability has an empty normalized name")
		}
		if _, ok := seen[item.Name]; ok {
			return fmt.Errorf("MCP capability name collision: %s", item.Name)
		}
		seen[item.Name] = struct{}{}
	}
	return nil
}

func (m *Manager) replaceServerTools(serverName string, tools []ToolSpec) error {
	if err := validateToolSpecs(tools); err != nil {
		return fmt.Errorf("validate MCP tools for server %q: %w", serverName, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := make(map[string]ToolSpec, len(m.tools)+len(tools))
	for name, item := range m.tools {
		if item.Server != serverName {
			next[name] = item
		}
	}
	for _, item := range tools {
		if existing, ok := next[item.Name]; ok && existing.Server != serverName {
			return fmt.Errorf("MCP capability %q from server %q collides with server %q", item.Name, serverName, existing.Server)
		}
		next[item.Name] = item.Clone()
	}
	m.tools = next
	m.snapshot.Version++
	m.snapshot.Tools = snapshotTools(m.tools)
	return nil
}

func snapshotTools(tools map[string]ToolSpec) []ToolSpec {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]ToolSpec, 0, len(names))
	for _, name := range names {
		result = append(result, tools[name].Clone())
	}
	return result
}

func (m *Manager) watchServer(name string, server *managedServer) {
	if server == nil || server.session == nil {
		return
	}
	go func() {
		for {
			select {
			case <-m.ctx.Done():
				return
			case notification := <-server.session.Notifications():
				switch notification.Method {
				case "notifications/tools/list_changed":
					m.refreshServer(name)
				case "notifications/resources/list_changed":
					m.refreshServer(name)
				case "notifications/prompts/list_changed":
					m.refreshServer(name)
				}
			}
		}
	}()
	go func() {
		err := server.session.WaitError()
		select {
		case <-m.ctx.Done():
			return
		default:
		}
		m.mu.Lock()
		if runtime := m.servers[name]; runtime != nil {
			runtime.status.State = "unavailable"
			runtime.status.LastError = formatProcessExit(err, runtime.session.StderrTail())
		}
		m.mu.Unlock()
		m.publishSnapshot()
	}()
}

func (m *Manager) refreshServer(name string) {
	server := m.server(name)
	if server == nil || server.session == nil {
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, callTimeout)
	defer cancel()
	// A refresh uses the same negotiated capability set as startup. MCP
	// capability-change notifications alter the list contents, not the
	// negotiated feature families.
	tools, counts, err := discoverCapabilities(ctx, name, server.session, server.capabilities)
	if err != nil {
		m.setServerError(name, err)
		return
	}
	if err := m.replaceServerTools(name, tools); err != nil {
		m.setServerError(name, err)
		return
	}
	m.mu.Lock()
	server.tools = tools
	server.status.Tools = counts.tools
	server.status.Resources = counts.resources
	server.status.Templates = counts.templates
	server.status.Prompts = counts.prompts
	server.status.BlockedTools = counts.blockedTools
	server.status.State = "running"
	server.status.LastError = ""
	m.mu.Unlock()
	m.publishSnapshot()
}

func (m *Manager) setServerError(name string, err error) {
	m.mu.Lock()
	if server := m.servers[name]; server != nil {
		server.status.LastError = truncateDiagnostic(err.Error())
	}
	m.mu.Unlock()
	m.publishSnapshot()
}

func (m *Manager) server(name string) *managedServer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.servers[name]
}

// Snapshot returns the current model-facing MCP capability snapshot.
func (m *Manager) Snapshot() Snapshot {
	if m == nil {
		return Snapshot{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot.Clone()
}

// Call invokes one namespaced MCP virtual tool. The name must exist in the
// latest snapshot; callers cannot use this broker to send arbitrary methods.
func (m *Manager) Call(ctx context.Context, qualifiedName string, input json.RawMessage) (string, error) {
	if m == nil {
		return "", errors.New("MCP manager is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.RLock()
	spec, ok := m.tools[strings.TrimSpace(qualifiedName)]
	server := m.servers[spec.Server]
	m.mu.RUnlock()
	if !ok || server == nil || server.session == nil {
		return "", fmt.Errorf("MCP tool %q is unavailable", qualifiedName)
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	var err error
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	switch spec.Kind {
	case KindTool:
		var result mcpToolCallResult
		err = server.session.Call(callCtx, "tools/call", map[string]any{
			"name":      spec.MCPName,
			"arguments": json.RawMessage(input),
		}, &result)
		if err == nil {
			return renderToolCallResult(result)
		}
	case KindListResources:
		var items []json.RawMessage
		items, err = listResources(callCtx, server.session)
		if err == nil {
			return marshalJSON(map[string]any{"resources": items})
		}
	case KindListTemplates:
		var items []json.RawMessage
		items, err = listTemplates(callCtx, server.session)
		if err == nil {
			return marshalJSON(map[string]any{"resourceTemplates": items})
		}
	case KindReadResource, KindGetPrompt:
		var result json.RawMessage
		err = server.session.Call(callCtx, spec.MCPName, json.RawMessage(input), &result)
		if err == nil {
			return renderGenericResult(spec.Kind, result)
		}
	case KindListPrompts:
		var items []json.RawMessage
		items, err = listPrompts(callCtx, server.session)
		if err == nil {
			return marshalJSON(map[string]any{"prompts": items})
		}
	default:
		return "", fmt.Errorf("MCP tool %q has unsupported capability kind %q", qualifiedName, spec.Kind)
	}
	return "", fmt.Errorf("MCP tool %q failed: %w", qualifiedName, err)
}

type mcpToolCallResult struct {
	Content           []json.RawMessage `json:"content"`
	IsError           bool              `json:"isError"`
	StructuredContent json.RawMessage   `json:"structuredContent"`
}

func renderToolCallResult(result mcpToolCallResult) (string, error) {
	if len(result.Content) == 1 && len(result.StructuredContent) == 0 {
		var block struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(result.Content[0], &block) == nil && block.Type == "text" {
			if result.IsError {
				return block.Text, fmt.Errorf("MCP tool reported an error: %s", block.Text)
			}
			return block.Text, nil
		}
	}
	rendered, err := marshalJSON(result)
	if err != nil {
		return "", err
	}
	if result.IsError {
		return rendered, fmt.Errorf("MCP tool reported an error: %s", rendered)
	}
	return rendered, nil
}

func renderGenericResult(kind CapabilityKind, result json.RawMessage) (string, error) {
	if kind != KindGetPrompt {
		if len(result) == 0 {
			return "null", nil
		}
		if !json.Valid(result) {
			return "", errors.New("MCP result is not valid JSON")
		}
		return string(result), nil
	}
	var prompt struct {
		Description string            `json:"description"`
		Messages    []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(result, &prompt); err != nil {
		return "", fmt.Errorf("decode MCP prompt result: %w", err)
	}
	lines := make([]string, 0, len(prompt.Messages)+1)
	if prompt.Description != "" {
		lines = append(lines, prompt.Description)
	}
	for _, raw := range prompt.Messages {
		var message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(raw, &message) != nil {
			lines = append(lines, string(raw))
			continue
		}
		var textBlock struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(message.Content, &textBlock) == nil && textBlock.Type == "text" {
			lines = append(lines, strings.TrimSpace(message.Role+": "+textBlock.Text))
		} else {
			lines = append(lines, strings.TrimSpace(message.Role+": "+string(message.Content)))
		}
	}
	if len(lines) == 0 {
		return string(result), nil
	}
	return strings.Join(lines, "\n"), nil
}

func marshalJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode MCP result: %w", err)
	}
	return string(data), nil
}

func (m *Manager) Status() []ServerStatus {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ServerStatus, 0, len(m.servers))
	for _, server := range m.servers {
		result = append(result, server.status)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (m *Manager) ConfigPath() string {
	if m == nil {
		return ""
	}
	return m.config.Path
}

// Subscribe returns an update stream for capability changes. The returned
// cancel function must be called when the consumer no longer needs updates.
func (m *Manager) Subscribe() (<-chan Snapshot, func()) {
	if m == nil {
		closed := make(chan Snapshot)
		close(closed)
		return closed, func() {}
	}
	channel := make(chan Snapshot, 1)
	m.mu.Lock()
	m.subs[channel] = struct{}{}
	current := m.snapshot.Clone()
	channel <- current
	m.mu.Unlock()
	return channel, func() {
		m.mu.Lock()
		if _, ok := m.subs[channel]; ok {
			delete(m.subs, channel)
			close(channel)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) publishSnapshot() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snapshot := m.snapshot.Clone()
	for channel := range m.subs {
		select {
		case channel <- snapshot:
		default:
		}
	}
}

func (m *Manager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.closeOnce.Do(func() {
		m.cancel()
		m.mu.RLock()
		servers := make([]*managedServer, 0, len(m.servers))
		for _, server := range m.servers {
			servers = append(servers, server)
		}
		m.mu.RUnlock()
		closeCtx, cancel := context.WithTimeout(ctx, closeTimeout)
		defer cancel()
		for _, server := range servers {
			if server.session == nil {
				continue
			}
			if err := server.session.Close(closeCtx); err != nil && m.closeErr == nil {
				m.closeErr = err
			}
		}
		m.mu.Lock()
		for channel := range m.subs {
			close(channel)
			delete(m.subs, channel)
		}
		m.mu.Unlock()
	})
	return m.closeErr
}

func startupError(config ServerConfig, phase string, err error, stderr string) error {
	detail := truncateDiagnostic(stderr)
	if detail != "" {
		return fmt.Errorf("MCP server %q (%s) failed during %s: %w; stderr: %s", config.Name, config.Command, phase, err, detail)
	}
	return fmt.Errorf("MCP server %q (%s) failed during %s: %w", config.Name, config.Command, phase, err)
}

func formatProcessExit(err error, stderr string) string {
	detail := strings.TrimSpace(stderr)
	if detail != "" {
		return truncateDiagnostic(detail)
	}
	if err == nil {
		return "MCP server exited"
	}
	return truncateDiagnostic(err.Error())
}

func truncateDiagnostic(value string) string {
	const max = 32 * 1024
	value = strings.TrimSpace(redactSensitiveText(value))
	if len(value) <= max {
		return value
	}
	return value[len(value)-max:]
}
