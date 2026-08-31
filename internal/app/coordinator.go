package app

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrActiveTurnChanged   = errors.New("active_turn_changed")
	ErrNoActiveTurn        = errors.New("no_active_turn")
	ErrInteractionNotFound = errors.New("interaction_not_found")
)

type WorkspaceCoordinator struct {
	mu    sync.Mutex
	state WorkspaceState
}

func NewWorkspaceCoordinator() *WorkspaceCoordinator {
	return &WorkspaceCoordinator{state: WorkspaceState{
		SessionVersion: make(map[string]uint64),
		Queue:          make(map[string][]InputDraft),
		Pending:        make(map[string]InteractionState),
		Parts:          make(map[string]StreamingPart),
	}}
}

func (c *WorkspaceCoordinator) BeginTurn(sessionID, turnID string) (WorkspaceState, error) {
	if c == nil {
		return WorkspaceState{}, errors.New("workspace coordinator is nil")
	}
	sessionID = strings.TrimSpace(sessionID)
	turnID = strings.TrimSpace(turnID)
	if sessionID == "" || turnID == "" {
		return WorkspaceState{}, errors.New("session ID and turn ID are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.ActiveTurnID != "" {
		return cloneWorkspaceState(c.state), fmt.Errorf("%w: active session %s turn %s", ErrWorkspaceBusy, c.state.ActiveSessionID, c.state.ActiveTurnID)
	}
	c.state.ActiveSessionID = sessionID
	c.state.ActiveTurnID = turnID
	c.bumpSessionVersionLocked(sessionID)
	return cloneWorkspaceState(c.state), nil
}

func (c *WorkspaceCoordinator) CompleteTurn(sessionID, turnID string) (WorkspaceState, error) {
	return c.finishTurn(sessionID, turnID)
}

func (c *WorkspaceCoordinator) FailTurn(sessionID, turnID string) (WorkspaceState, error) {
	return c.finishTurn(sessionID, turnID)
}

func (c *WorkspaceCoordinator) CancelTurn(sessionID, turnID string) (WorkspaceState, error) {
	return c.finishTurn(sessionID, turnID)
}

func (c *WorkspaceCoordinator) finishTurn(sessionID, turnID string) (WorkspaceState, error) {
	if c == nil {
		return WorkspaceState{}, errors.New("workspace coordinator is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateActiveTurnLocked(sessionID, turnID); err != nil {
		return cloneWorkspaceState(c.state), err
	}
	deletePendingTurnLocked(c.state.Pending, turnID)
	c.state.ActiveSessionID = ""
	c.state.ActiveTurnID = ""
	c.bumpSessionVersionLocked(sessionID)
	return cloneWorkspaceState(c.state), nil
}

func (c *WorkspaceCoordinator) Steer(sessionID, turnID string) error {
	if c == nil {
		return errors.New("workspace coordinator is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.validateActiveTurnLocked(sessionID, turnID)
}

func (c *WorkspaceCoordinator) QueueInput(sessionID, turnID string, draft InputDraft) (WorkspaceState, error) {
	if c == nil {
		return WorkspaceState{}, errors.New("workspace coordinator is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateActiveTurnLocked(sessionID, turnID); err != nil {
		return cloneWorkspaceState(c.state), err
	}
	c.state.Queue[sessionID] = append(c.state.Queue[sessionID], draft)
	c.bumpSessionVersionLocked(sessionID)
	return cloneWorkspaceState(c.state), nil
}

func (c *WorkspaceCoordinator) DequeueInput(sessionID string) (InputDraft, WorkspaceState, bool) {
	if c == nil {
		return InputDraft{}, WorkspaceState{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	queue := c.state.Queue[sessionID]
	if len(queue) == 0 {
		return InputDraft{}, cloneWorkspaceState(c.state), false
	}
	draft := queue[0]
	if len(queue) == 1 {
		delete(c.state.Queue, sessionID)
	} else {
		c.state.Queue[sessionID] = append([]InputDraft(nil), queue[1:]...)
	}
	c.bumpSessionVersionLocked(sessionID)
	return draft, cloneWorkspaceState(c.state), true
}

func (c *WorkspaceCoordinator) AddInteraction(interaction InteractionState) (WorkspaceState, error) {
	if c == nil {
		return WorkspaceState{}, errors.New("workspace coordinator is nil")
	}
	interaction.RequestID = strings.TrimSpace(interaction.RequestID)
	interaction.SessionID = strings.TrimSpace(interaction.SessionID)
	if interaction.RequestID == "" || interaction.SessionID == "" {
		return WorkspaceState{}, errors.New("interaction request ID and session ID are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if interaction.TurnID != "" {
		if err := c.validateActiveTurnLocked(interaction.SessionID, interaction.TurnID); err != nil {
			return cloneWorkspaceState(c.state), err
		}
	}
	c.state.Pending[interaction.RequestID] = interaction
	c.bumpSessionVersionLocked(interaction.SessionID)
	return cloneWorkspaceState(c.state), nil
}

func (c *WorkspaceCoordinator) ResolveInteraction(requestID string) (InteractionState, WorkspaceState, error) {
	if c == nil {
		return InteractionState{}, WorkspaceState{}, errors.New("workspace coordinator is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	interaction, ok := c.state.Pending[requestID]
	if !ok {
		return InteractionState{}, cloneWorkspaceState(c.state), ErrInteractionNotFound
	}
	delete(c.state.Pending, requestID)
	c.bumpSessionVersionLocked(interaction.SessionID)
	return interaction, cloneWorkspaceState(c.state), nil
}

func (c *WorkspaceCoordinator) StartPart(part StreamingPart) (WorkspaceState, error) {
	if c == nil {
		return WorkspaceState{}, errors.New("workspace coordinator is nil")
	}
	part.PartID = strings.TrimSpace(part.PartID)
	if part.PartID == "" {
		return WorkspaceState{}, errors.New("part ID is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateActiveTurnLocked(part.SessionID, part.TurnID); err != nil {
		return cloneWorkspaceState(c.state), err
	}
	if _, exists := c.state.Parts[part.PartID]; exists {
		return cloneWorkspaceState(c.state), fmt.Errorf("part already exists: %s", part.PartID)
	}
	c.state.Parts[part.PartID] = part
	return cloneWorkspaceState(c.state), nil
}

func (c *WorkspaceCoordinator) AppendPart(partID, text string) (StreamingPart, WorkspaceState, error) {
	if c == nil {
		return StreamingPart{}, WorkspaceState{}, errors.New("workspace coordinator is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	part, ok := c.state.Parts[partID]
	if !ok {
		return StreamingPart{}, cloneWorkspaceState(c.state), fmt.Errorf("part not found: %s", partID)
	}
	if part.Completed {
		return part, cloneWorkspaceState(c.state), fmt.Errorf("part already completed: %s", partID)
	}
	part.Text += text
	c.state.Parts[partID] = part
	return part, cloneWorkspaceState(c.state), nil
}

func (c *WorkspaceCoordinator) CompletePart(partID string) (StreamingPart, WorkspaceState, error) {
	if c == nil {
		return StreamingPart{}, WorkspaceState{}, errors.New("workspace coordinator is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	part, ok := c.state.Parts[partID]
	if !ok {
		return StreamingPart{}, cloneWorkspaceState(c.state), fmt.Errorf("part not found: %s", partID)
	}
	part.Completed = true
	c.state.Parts[partID] = part
	return part, cloneWorkspaceState(c.state), nil
}

func (c *WorkspaceCoordinator) SetActiveTasks(count int) WorkspaceState {
	if c == nil {
		return WorkspaceState{}
	}
	if count < 0 {
		count = 0
	}
	c.mu.Lock()
	c.state.ActiveTasks = count
	state := cloneWorkspaceState(c.state)
	c.mu.Unlock()
	return state
}

func (c *WorkspaceCoordinator) SetActiveWrites(count int) WorkspaceState {
	if c == nil {
		return WorkspaceState{}
	}
	if count < 0 {
		count = 0
	}
	c.mu.Lock()
	c.state.ActiveWrites = count
	state := cloneWorkspaceState(c.state)
	c.mu.Unlock()
	return state
}

func (c *WorkspaceCoordinator) ConsistentSnapshot(hub *EventHub) CoordinatorSnapshot {
	if c == nil {
		return CoordinatorSnapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var cursor EventCursor
	if hub != nil {
		cursor = hub.CurrentCursor()
	}
	return CoordinatorSnapshot{State: cloneWorkspaceState(c.state), Cursor: cursor}
}

func (c *WorkspaceCoordinator) WorkspaceSnapshot() WorkspaceState {
	if c == nil {
		return WorkspaceState{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneWorkspaceState(c.state)
}

func (c *WorkspaceCoordinator) SessionSnapshot(sessionID string) SessionState {
	if c == nil {
		return SessionState{SessionID: sessionID}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	pending := make([]InteractionState, 0)
	for _, interaction := range c.state.Pending {
		if interaction.SessionID == sessionID {
			pending = append(pending, interaction)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].RequestID < pending[j].RequestID })
	return SessionState{
		SessionID:      sessionID,
		SessionVersion: c.state.SessionVersion[sessionID],
		Queue:          append([]InputDraft(nil), c.state.Queue[sessionID]...),
		Pending:        pending,
	}
}

func (c *WorkspaceCoordinator) Activity() RuntimeActivity {
	if c == nil {
		return RuntimeActivity{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	queued := 0
	for _, items := range c.state.Queue {
		queued += len(items)
	}
	return RuntimeActivity{
		ActiveTurn:          boolInt(c.state.ActiveTurnID != ""),
		ActiveTasks:         c.state.ActiveTasks,
		PendingInteractions: len(c.state.Pending),
		QueuedInputs:        queued,
		ActiveWrites:        c.state.ActiveWrites,
	}
}

func (c *WorkspaceCoordinator) validateActiveTurnLocked(sessionID, turnID string) error {
	if c.state.ActiveTurnID == "" {
		return ErrNoActiveTurn
	}
	if c.state.ActiveSessionID != strings.TrimSpace(sessionID) || c.state.ActiveTurnID != strings.TrimSpace(turnID) {
		return fmt.Errorf("%w: active session %s turn %s", ErrActiveTurnChanged, c.state.ActiveSessionID, c.state.ActiveTurnID)
	}
	return nil
}

func (c *WorkspaceCoordinator) bumpSessionVersionLocked(sessionID string) {
	if sessionID != "" {
		c.state.SessionVersion[sessionID]++
	}
}

func deletePendingTurnLocked(pending map[string]InteractionState, turnID string) {
	for requestID, interaction := range pending {
		if interaction.TurnID == turnID {
			delete(pending, requestID)
		}
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
