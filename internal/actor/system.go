package actor

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"
)

// System 是组合根（spec §5 上帝类防线：仅做组合，逻辑在各组件）：
// Router + Scheduler + Journal + Supervisor + Snapshotter(经 cell)。
type System struct {
	dir       string
	clock     Clock
	providers map[string]func(ActorID) Actor
	streams   map[string]StreamStore

	router     *router
	scheduler  *scheduler
	journal    *journal
	supervisor *supervisor
	wheel      *wheel

	mailboxCap       int
	snapshotInterval int
	passivateAfter   time.Duration
	logger           func(format string, args ...any)

	stopOnce sync.Once
	stopped  chan struct{}

	mu       sync.Mutex
	promises map[string]chan Msg
}

// Option 配置 System。
type Option func(*System)

// WithShards 固定分片数（默认 CPU×4；确定性测试用 1）。
func WithShards(n int) Option { return func(s *System) { s.scheduler = newScheduler(n) } }

// WithClock 替换时钟（虚拟时钟驱动确定性测试）。
func WithClock(c Clock) Option { return func(s *System) { s.clock = c } }

// WithPassivation 设置钝化空闲阈值（≤0 关闭；默认 5min）。
func WithPassivation(d time.Duration) Option { return func(s *System) { s.passivateAfter = d } }

// WithSnapshotInterval 设置快照间隔事件数（默认 200，对齐 es）。
func WithSnapshotInterval(n int) Option { return func(s *System) { s.snapshotInterval = n } }

// WithMailboxCap 设置邮箱上限（默认 256，spec §10）。
func WithMailboxCap(n int) Option { return func(s *System) { s.mailboxCap = n } }

// WithSupervision 设置监督窗口与隔离阈值。
func WithSupervision(window time.Duration, maxPanics int) Option {
	return func(s *System) { s.supervisor = newSupervisor(window, maxPanics) }
}

// WithLogger 注入日志函数（默认标准日志）。
func WithLogger(fn func(format string, args ...any)) Option { return func(s *System) { s.logger = fn } }

// WithStreamStore overrides persistence for one actor type. Other actor types
// continue using the runtime's default per-type es.JSONL streams.
func WithStreamStore(actorType string, store StreamStore) Option {
	return func(s *System) {
		if actorType != "" && store != nil {
			s.streams[actorType] = store
		}
	}
}

// NewSystem 在 dir 下构建运行时（流路径 {dir}/{type}/{key}.events.jsonl）。
func NewSystem(dir string, opts ...Option) *System {
	s := &System{
		dir:              dir,
		clock:            RealClock{},
		providers:        make(map[string]func(ActorID) Actor),
		streams:          make(map[string]StreamStore),
		mailboxCap:       defaultMailboxCap,
		snapshotInterval: 200,
		passivateAfter:   5 * time.Minute,
		logger:           log.Printf,
		stopped:          make(chan struct{}),
		promises:         make(map[string]chan Msg),
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.scheduler == nil {
		s.scheduler = newScheduler(runtime.NumCPU() * 4)
	}
	if s.supervisor == nil {
		s.supervisor = newSupervisor(time.Minute, 3)
	}
	s.router = newRouter()
	s.journal = newJournal(dir, s.clock, s.streams)
	s.wheel = newWheel(s.clock, s)
	return s
}

// Register 注册某类型 actor 的构造器（幂等激活的 provider）。
func (s *System) Register(actorType string, provider func(ActorID) Actor) *System {
	s.providers[actorType] = provider
	return s
}

// Ref 返回 id 的地址句柄。
func (s *System) Ref(id ActorID) Ref { return sysRef{s: s, id: id} }

// Tell 幂等激活 + 投递（at-least-once）。
func (s *System) Tell(ctx context.Context, id ActorID, msg Msg) error {
	select {
	case <-s.stopped:
		return ErrStopped
	default:
	}
	return s.route(id, msg)
}

// route 是统一投递入口：激活（若需）→ 入邮箱 → 泵送。
func (s *System) route(id ActorID, msg Msg) error {
	if err := id.Validate(); err != nil {
		return err
	}
	if _, ok := s.providers[id.Type]; !ok {
		return fmt.Errorf("%w: %s", ErrNoProvider, id.Type)
	}
	c := s.router.ensure(s, id)
	if c.quarantine.Load() {
		return ErrDeadLettered
	}
	if err := c.mbox.push(delivery{msg: msg}); err != nil {
		return err
	}
	c.ensureActivated(context.Background())
	c.pump()
	return nil
}

// routeAfterRecovery 是激活扫描的 Outbox 补发：目标未注册时不报错
// （跳过；目标激活时自身账本兜底）。
func (s *System) routeAfterRecovery(id ActorID, msg Msg) {
	if err := s.route(id, msg); err != nil {
		s.logger("actor: outbox recovery to %s: %v", id, err)
	}
}

// DeadLetters 返回监督隔离记录。
func (s *System) DeadLetters() []DeadLetter { return s.supervisor.DeadLetters() }

// Drain 等待所有分片排空到稳定（确定性测试同步点）：处理期间派生的
// 新任务会被下一轮屏障捕获，直到无新任务产生。
func (s *System) Drain() {
	for i := 0; i < 10000; i++ {
		<-s.scheduler.drainBarrier()
		if s.scheduler.quiescentNow() {
			return
		}
	}
}

// Stop 排空后停机：活跃 cell 尽力写终态快照。
func (s *System) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopped)
		s.Drain()
		for _, c := range s.router.snapshot() {
			c.snapshot(context.Background())
			c.mbox.close()
			s.wheel.drop(c.id)
		}
		s.scheduler.stop()
	})
}

// SetJournalHook 注册 journal 协议位点钩子（崩溃注入/指标埋点；nil 清除）。
func (s *System) SetJournalHook(fn func(stage string, id ActorID)) {
	s.journal.setHook(fn)
}

// submitCell 把任务排到 cell 所属分片（保 I1 串行）。
func (s *System) submitCell(c *cell, task func()) { s.scheduler.submit(c.id, task) }

// submitByID 同上（按 id 哈希）。
func (s *System) submitByID(id ActorID, task func()) { s.scheduler.submit(id, task) }

// once 挂在 cell 上（ctx.Once 委托）。
func (c *cell) once(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ledger == nil {
		return false
	}
	if c.ledger.once[key] {
		return false
	}
	c.ledger.once[key] = true
	return true
}
