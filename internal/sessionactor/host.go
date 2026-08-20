package sessionactor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"paw/internal/actor"
	"paw/internal/loop"
	"paw/internal/message"
	"paw/internal/session"
)

const hostAskTimeout = 7 * 24 * time.Hour

// Host is the production turn seam. Engine configuration methods remain
// available through embedding, while turn execution always crosses the actor.
type Host struct {
	*loop.Engine
	store  *session.JSONLStore
	system *actor.System

	mu                sync.RWMutex
	executeMu         sync.Mutex
	sessionID         string
	contexts          sync.Map
	permissionPrompts sync.Map
	prompter          PermissionPrompter
	closing           chan struct{}
	closeOnce         sync.Once
}

func NewHost(engine *loop.Engine, store *session.JSONLStore, sessionID string) (*Host, error) {
	if engine == nil || store == nil || sessionID == "" {
		return nil, fmt.Errorf("session actor host requires engine, store, and session id")
	}
	host := &Host{Engine: engine, store: store, sessionID: sessionID, closing: make(chan struct{})}
	host.system = actor.NewSystem(filepath.Join(store.Dir(), "actors"), actor.WithStreamStore(actorType, sessionStream{store: store}))
	host.system.Register(actorType, func(id actor.ActorID) actor.Actor { return newSessionActor(id, host) })
	engine.SetPermissionGate(host)
	engine.SetToolLifecycle(host)
	return host, nil
}

func (h *Host) Close() {
	if h == nil {
		return
	}
	h.closeOnce.Do(func() {
		// 先关闭 closing：Republish 重发协程不再发起新的 Decide。
		close(h.closing)
		h.system.Stop()
	})
}

func (h *Host) RunTurn(ctx context.Context, input string) (message.Message, error) {
	execution, err := h.run(ctx, message.Message{Role: message.RoleUser, Content: input}, "", time.Time{}, false)
	return execution.Message, err
}

func (h *Host) RunRichTurn(ctx context.Context, input message.Message) (message.Message, error) {
	execution, err := h.run(ctx, input, "", time.Time{}, false)
	return execution.Message, err
}

func (h *Host) persistInputAttachments(ctx context.Context, input *message.Message) error {
	if h == nil || input == nil || len(input.Parts) == 0 {
		return nil
	}
	for i := range input.Parts {
		part := &input.Parts[i]
		if part.Type != message.ContentPartImage || part.Image == nil {
			continue
		}
		if len(part.Image.Data) == 0 {
			if strings.TrimSpace(part.Image.Attachment) == "" {
				return fmt.Errorf("图片附件缺少数据或引用")
			}
			continue
		}
		reference, err := h.store.SaveAttachment(ctx, part.Image.MIMEType, part.Image.Data)
		if err != nil {
			return fmt.Errorf("保存图片附件失败: %w", err)
		}
		part.Image.Attachment = reference
	}
	return nil
}

func (h *Host) RunTurnWithTiming(ctx context.Context, input, turnID string, startedAt time.Time) (loop.TurnExecution, error) {
	return h.run(ctx, message.Message{Role: message.RoleUser, Content: input}, turnID, startedAt, false)
}

func (h *Host) RunRichTurnWithTiming(ctx context.Context, input message.Message, turnID string, startedAt time.Time) (loop.TurnExecution, error) {
	return h.run(ctx, input, turnID, startedAt, false)
}

func (h *Host) ExecuteTurn(ctx context.Context, input message.Message, timing *loop.TurnTiming) (loop.TurnExecution, error) {
	if timing == nil {
		timing = &loop.TurnTiming{}
	}
	return h.run(ctx, input, timing.TurnID, timing.StartedAt, true)
}

func (h *Host) GoalTurnExecutor() loop.TurnExecutor { return h }

func (h *Host) run(ctx context.Context, input message.Message, turnID string, startedAt time.Time, single bool) (loop.TurnExecution, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if input.Role == "" {
		input.Role = message.RoleUser
	}
	if input.Role != message.RoleUser {
		return loop.TurnExecution{}, fmt.Errorf("rich turn 必须使用 user role")
	}
	if err := h.persistInputAttachments(ctx, &input); err != nil {
		return loop.TurnExecution{}, err
	}
	if turnID == "" {
		id, err := session.GenerateSessionID()
		if err != nil {
			return loop.TurnExecution{}, err
		}
		turnID = "turn-" + id
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	msgID := "turn:" + turnID
	h.contexts.Store(msgID, ctx)
	defer h.contexts.Delete(msgID)
	reply, err := h.system.Ref(actor.ActorID{Type: actorType, Key: h.CurrentSessionID()}).Ask(ctx, actor.Msg{
		MsgID: msgID, Kind: msgRunTurn,
		Payload:    turnRequest{Input: input, Timing: loop.TurnTiming{TurnID: turnID, StartedAt: startedAt}, Single: single},
		Durability: actor.Durable,
	}, hostAskTimeout)
	if err != nil {
		return loop.TurnExecution{}, err
	}
	result, ok := reply.Payload.(turnReply)
	if !ok {
		return loop.TurnExecution{}, fmt.Errorf("invalid session actor turn reply %T", reply.Payload)
	}
	if result.Error != "" {
		return result.Execution, fmt.Errorf("%s", result.Error)
	}
	return result.Execution, nil
}

func (h *Host) execute(sessionID, msgID string, request turnRequest) (loop.TurnExecution, error) {
	h.executeMu.Lock()
	defer h.executeMu.Unlock()
	if err := h.recoverUnknownToolStarts(context.Background(), sessionID); err != nil {
		return loop.TurnExecution{}, err
	}
	if recovered, stop, err := h.recoveredTurnDisposition(context.Background(), sessionID, request.Timing.TurnID); stop {
		return recovered, err
	}
	if h.CurrentSessionID() != sessionID {
		if _, err := h.Engine.LoadSession(context.Background(), sessionID); err != nil {
			return loop.TurnExecution{}, err
		}
		h.mu.Lock()
		h.sessionID = sessionID
		h.mu.Unlock()
	}
	ctx := context.Background()
	if value, ok := h.contexts.Load(msgID); ok {
		ctx, _ = value.(context.Context)
	}
	if request.Single {
		return h.Engine.ExecuteTurn(ctx, request.Input, &request.Timing)
	}
	return h.Engine.RunRichTurnWithTiming(ctx, request.Input, request.Timing.TurnID, request.Timing.StartedAt)
}

func (h *Host) recoveredTurnDisposition(ctx context.Context, sessionID, turnID string) (loop.TurnExecution, bool, error) {
	if turnID == "" {
		return loop.TurnExecution{}, false, nil
	}
	records, err := h.store.LoadResolvedJournalRecords(ctx, sessionID)
	if err != nil {
		return loop.TurnExecution{}, true, err
	}
	started := false
	completed := false
	partial := false
	var assistant message.Message
	var results []message.ToolResult
	for _, record := range records {
		if record.TurnID != turnID {
			continue
		}
		switch record.Kind {
		case session.JournalTurnStarted:
			started = true
		case session.JournalAssistant:
			assistant = message.CloneMessage(record.Message)
		case session.JournalAssistantPartial:
			assistant = message.CloneMessage(record.Message)
			partial = meaningfulAssistant(assistant)
		case session.JournalToolResult:
			if record.ToolResult != nil {
				results = append(results, *record.ToolResult)
			}
		case session.JournalTurnCompleted:
			completed = true
		}
	}
	if !started {
		return loop.TurnExecution{}, false, nil
	}
	if completed {
		return loop.TurnExecution{Message: assistant}, true, nil
	}
	if partial {
		return loop.TurnExecution{Message: assistant}, true, fmt.Errorf("turn %s was interrupted after meaningful output and will not be replayed", turnID)
	}
	if !meaningfulAssistant(assistant) {
		return loop.TurnExecution{}, false, nil
	}
	calls := assistantToolCalls(assistant)
	for _, result := range results {
		if result.IsError && strings.Contains(result.Content, "side effects are unknown") {
			return loop.TurnExecution{Message: assistant}, true, fmt.Errorf("turn %s was interrupted after a tool started with unknown side effects", turnID)
		}
	}
	if len(calls) == 0 {
		if err := h.store.CompleteTurn(context.WithoutCancel(ctx), sessionID, turnID); err != nil {
			return loop.TurnExecution{}, true, err
		}
		return loop.TurnExecution{Message: assistant}, true, nil
	}
	snapshot, err := h.store.LoadSnapshot(ctx, sessionID)
	if err != nil {
		return loop.TurnExecution{}, true, err
	}
	execution, err := h.Engine.ContinueRecoveredTurn(ctx, loop.RecoveredTurn{
		TurnID: turnID, History: snapshot.ActiveHistory,
		Assistant: assistant, CompletedResult: results,
	}, &loop.TurnTiming{TurnID: turnID})
	return execution, true, err
}

func meaningfulAssistant(msg message.Message) bool {
	return msg.Content != "" || len(msg.AssistantParts) > 0 || msg.ToolUse != nil || len(msg.ToolUses) > 0 || len(msg.ProviderData) > 0
}

func assistantToolCalls(msg message.Message) []message.ToolCall {
	if len(msg.AssistantParts) > 0 {
		var calls []message.ToolCall
		for _, part := range msg.AssistantParts {
			if part.ToolCall != nil {
				calls = append(calls, *part.ToolCall)
			}
		}
		return calls
	}
	if len(msg.ToolUses) > 0 {
		return append([]message.ToolCall(nil), msg.ToolUses...)
	}
	if msg.ToolUse != nil {
		return []message.ToolCall{*msg.ToolUse}
	}
	return nil
}

func (h *Host) CurrentSessionID() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessionID
}

func (h *Host) LoadSession(ctx context.Context, sessionID string) (loop.SessionLoadResult, error) {
	h.mu.Lock()
	previous := h.sessionID
	h.sessionID = sessionID
	h.mu.Unlock()
	if err := h.recoverUnknownToolStarts(ctx, sessionID); err != nil {
		return loop.SessionLoadResult{}, err
	}
	result, err := h.Engine.LoadSession(ctx, sessionID)
	if err != nil {
		h.mu.Lock()
		h.sessionID = previous
		h.mu.Unlock()
		return result, err
	}
	result.Modes, err = h.SessionModes(ctx, sessionID)
	if err != nil {
		return result, err
	}
	if err := h.system.Activate(actor.ActorID{Type: actorType, Key: sessionID}); err != nil {
		return result, err
	}
	h.RepublishPendingPermissions(sessionID)
	return result, nil
}

func (h *Host) SessionModes(ctx context.Context, sessionID string) (*loop.SessionModeSnapshot, error) {
	state, err := h.State(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	modes := &loop.SessionModeSnapshot{
		ActiveGoalID: state.ActiveGoalID, GoalStatus: state.GoalStatus,
		ActivePlanID: state.ActivePlanID, PlanStatus: state.PlanStatus,
	}
	for _, permission := range state.Permissions {
		if permission.Decision == "" {
			modes.PendingPermissionID = permission.ID
			break
		}
	}
	return modes, nil
}

func (h *Host) LoadHistory(ctx context.Context, sessionID string) ([]message.Message, error) {
	result, err := h.LoadSession(ctx, sessionID)
	return result.Messages, err
}

func (h *Host) State(ctx context.Context, sessionID string) (State, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	envelopes, _, err := h.store.LoadEnvelopes(ctx, sessionID)
	if err != nil {
		return State{}, err
	}
	folded := newSessionActor(actor.ActorID{Type: actorType, Key: sessionID}, h)
	for _, env := range envelopes {
		if err := folded.Fold(env); err != nil {
			return State{}, fmt.Errorf("fold session state: %w", err)
		}
	}
	return folded.state.clone(), nil
}

func (h *Host) ActiveGoal(ctx context.Context, sessionID string) (string, string, error) {
	state, err := h.State(ctx, sessionID)
	return state.ActiveGoalID, state.GoalStatus, err
}

func (h *Host) ActivePlan(ctx context.Context, sessionID string) (string, string, json.RawMessage, error) {
	state, err := h.State(ctx, sessionID)
	return state.ActivePlanID, state.PlanStatus, append(json.RawMessage(nil), state.PlanSnapshot...), err
}

type PermissionPrompter interface {
	PromptPermission(context.Context, loop.PermissionRequest) (loop.PermissionDecision, error)
}

func (h *Host) SetPermissionPrompter(prompter PermissionPrompter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prompter = prompter
}

func (h *Host) RepublishPendingPermissions(sessionID string) {
	if h == nil || sessionID == "" {
		return
	}
	h.mu.RLock()
	prompter := h.prompter
	h.mu.RUnlock()
	if prompter == nil {
		return
	}
	state, err := h.State(context.Background(), sessionID)
	if err != nil {
		return
	}
	for _, permission := range state.Permissions {
		if permission.Decision != "" {
			continue
		}
		if _, loaded := h.permissionPrompts.LoadOrStore(permission.ID, struct{}{}); loaded {
			continue
		}
		request := permission.Request
		go func(id string) {
			defer h.permissionPrompts.Delete(id)
			// 生命周期守卫：Host 已关闭时不再发起新的审批提示
			// （否则会在停机的 system 上做 Suspend/Activate 空转）。
			select {
			case <-h.closing:
				return
			default:
			}
			_, _ = h.Decide(context.Background(), request)
		}(permission.ID)
	}
}

func (h *Host) Decide(ctx context.Context, request loop.PermissionRequest) (loop.PermissionDecision, error) {
	id := permissionID(request)
	actorID := actor.ActorID{Type: actorType, Key: request.SessionID}
	state, err := h.State(ctx, request.SessionID)
	if err != nil {
		return "", err
	}
	if index := state.permissionIndex(id); index >= 0 {
		permission := state.Permissions[index]
		if permission.Decision != "" {
			if err := h.deliverPermissionDecision(request.SessionID, id, permissionRecord{ID: id, Request: permission.Request, Decision: permission.Decision, At: time.Now().UTC()}); err != nil {
				return "", err
			}
			return permission.Decision, nil
		}
	} else {
		if err := h.system.Suspend(actorID, "awaiting Read permission "+id); err != nil {
			return "", err
		}
		if err := h.appendDomain(ctx, request.SessionID, EventPermissionRequested, permissionRecord{ID: id, Request: request, At: time.Now().UTC()}); err != nil {
			// 补偿：Suspend 已生效但请求事件未落盘。不 Resume 则 actor
			// 永久滞留挂起态，且重启后没有 pending 权限可重发提示。
			_ = h.system.Resume(actorID)
			return "", err
		}
	}
	h.mu.RLock()
	prompter := h.prompter
	h.mu.RUnlock()
	decision := loop.PermissionDeny
	if prompter != nil {
		decision, err = prompter.PromptPermission(ctx, request)
		if err != nil {
			// 补偿：提示失败不落 decided 事件，但必须解除挂起，否则
			// 后续 turn 的 Ask 会滞留邮箱直到超时。权限保持 pending，
			// 重启/下次 LoadSession 时 Republish 会重新提示。
			_ = h.system.Resume(actorID)
			return "", err
		}
	}
	if decision != loop.PermissionAllowOnce && decision != loop.PermissionDeny {
		return "", fmt.Errorf("invalid permission decision %q", decision)
	}
	if err := h.system.Activate(actorID); err != nil {
		return "", err
	}
	if err := h.appendDomain(context.WithoutCancel(ctx), request.SessionID, EventPermissionDecided, permissionRecord{ID: id, Request: request, Decision: decision, At: time.Now().UTC()}); err != nil {
		return "", err
	}
	if err := h.deliverPermissionDecision(request.SessionID, id, permissionRecord{ID: id, Request: request, Decision: decision, At: time.Now().UTC()}); err != nil {
		return "", err
	}
	return decision, nil
}

func (h *Host) deliverPermissionDecision(sessionID, id string, record permissionRecord) error {
	actorID := actor.ActorID{Type: actorType, Key: sessionID}
	if err := h.system.Activate(actorID); err != nil {
		return err
	}
	if err := h.system.Ref(actorID).Tell(context.Background(), actor.Msg{
		MsgID: "decision:" + id, Kind: msgPermissionDecision,
		Payload: record, Durability: actor.Durable,
	}); err != nil {
		return err
	}
	return h.system.Resume(actorID)
}

func permissionID(request loop.PermissionRequest) string {
	digest := sha256.Sum256([]byte(request.TurnID + "\x00" + request.ToolCallID + "\x00" + request.CanonicalPath))
	return fmt.Sprintf("permission-%x", digest[:12])
}

func (h *Host) appendDomain(ctx context.Context, sessionID, eventType string, payload any) error {
	return h.system.PersistDomain(ctx, actor.ActorID{Type: actorType, Key: sessionID}, eventType, payload)
}

func (h *Host) ToolStarted(ctx context.Context, started loop.ToolStart) error {
	return h.appendDomain(context.WithoutCancel(ctx), started.SessionID, EventToolStarted, started)
}

func (h *Host) recoverUnknownToolStarts(ctx context.Context, sessionID string) error {
	state, err := h.State(ctx, sessionID)
	if err != nil {
		return err
	}
	failedTurns := map[string]bool{}
	for _, started := range state.StartedTools {
		result := message.ToolResult{
			ToolUseID: started.ToolCallID,
			Content:   "interrupted: tool started before restart; side effects are unknown and the call will not be retried",
			IsError:   true,
		}
		if err := h.store.AppendToolResult(context.WithoutCancel(ctx), sessionID, started.TurnID, started.CallIndex, result); err != nil {
			return err
		}
		failedTurns[started.TurnID] = true
	}
	for turnID := range failedTurns {
		failure := fmt.Errorf("turn interrupted after tool start with unknown side effects")
		if err := h.store.FailTurn(context.WithoutCancel(ctx), sessionID, turnID, failure); err != nil {
			return err
		}
		if err := h.appendDomain(context.WithoutCancel(ctx), sessionID, EventTurnInterrupted, interruptedTurn{TurnID: turnID, Error: failure.Error(), At: time.Now().UTC()}); err != nil {
			return err
		}
	}
	return nil
}

func (h *Host) ActivateGoal(ctx context.Context, id, status string) error {
	return h.ActivateGoalFor(ctx, h.CurrentSessionID(), id, status)
}
func (h *Host) ClearGoal(ctx context.Context) error {
	return h.ClearGoalFor(ctx, h.CurrentSessionID())
}
func (h *Host) SavePlan(ctx context.Context, id, status string, snapshot any) error {
	return h.SavePlanFor(ctx, h.CurrentSessionID(), id, status, snapshot)
}
func (h *Host) ClearPlan(ctx context.Context) error {
	return h.ClearPlanFor(ctx, h.CurrentSessionID())
}

func (h *Host) ActivateGoalFor(ctx context.Context, sessionID, id, status string) error {
	return h.appendDomain(ctx, sessionID, EventGoalActivated, goalBinding{GoalID: id, Status: status})
}
func (h *Host) ClearGoalFor(ctx context.Context, sessionID string) error {
	return h.appendDomain(ctx, sessionID, EventGoalCleared, map[string]string{})
}
func (h *Host) SavePlanFor(ctx context.Context, sessionID, id, status string, snapshot any) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return h.appendDomain(ctx, sessionID, EventPlanSnapshot, planBinding{PlanID: id, Status: status, Snapshot: raw})
}
func (h *Host) ClearPlanFor(ctx context.Context, sessionID string) error {
	return h.appendDomain(ctx, sessionID, EventPlanCleared, map[string]string{})
}

func (h *Host) mutate(ctx context.Context, kind string, payload any) error {
	return h.mutateFor(ctx, h.CurrentSessionID(), kind, payload)
}

func (h *Host) mutateFor(ctx context.Context, sessionID, kind string, payload any) error {
	reply, err := h.system.Ref(actor.ActorID{Type: actorType, Key: sessionID}).Ask(ctx, actor.Msg{Kind: kind, Payload: payload, Durability: actor.Durable}, time.Minute)
	if err != nil {
		return err
	}
	return replyErrorOf(reply.Payload)
}

// replyErrorOf 解析 goal/plan 变更应答中的错误（mutationError 载体；
// 成功应答是 State 投影，不含 error 字段）。
func replyErrorOf(payload any) error {
	if payload == nil {
		return nil
	}
	if failure, ok := payload.(mutationError); ok {
		if failure.Error != "" {
			return fmt.Errorf("%s", failure.Error)
		}
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var probe struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &probe) != nil || probe.Error == "" {
		return nil
	}
	return fmt.Errorf("%s", probe.Error)
}
