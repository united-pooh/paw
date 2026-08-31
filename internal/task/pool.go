package task

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	randv2 "math/rand/v2"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	coremcp "paw/internal/mcp"
	"paw/internal/tokentracer"

	"github.com/sourcegraph/conc"
)

const (
	defaultPoolWorkerCount = 4
	defaultPoolQueueSize   = 8
	poolReadyTimeout       = 30 * time.Second
	poolStderrLimit        = 64 * 1024
)

// ProcessPoolLauncher keeps a bounded set of long-lived worker processes and
// exposes the same job-oriented Launcher contract as ProcessLauncher. A worker
// handles one job at a time and is returned to the pool only after its result
// has been received.
type ProcessPoolLauncher struct {
	mu sync.Mutex

	Command string
	Args    []string
	Dir     string
	Env     []string
	Broker  coremcp.Broker

	MaxWorkers    int
	QueueCapacity int

	// JobWallTime 是单任务最大墙钟时长；0 表示不设上限（默认不启用）。
	JobWallTime time.Duration

	// roleOrder/roleCursor 让池内 worker 从角色池随机且不重复地获得身份。
	roleOrder  []int
	roleCursor int

	submit    chan *poolJob
	shutdown  chan struct{}
	wake      chan struct{}
	slots     chan struct{}
	startOnce sync.Once
	closeOnce sync.Once

	group       *conc.WaitGroup
	workerGroup *conc.WaitGroup
	poolCtx     context.Context
	poolCancel  context.CancelCauseFunc
	closed      bool
	closeErr    error
}

// NewProcessPoolLauncher creates a lazy process pool. The caller may replace
// Args in tests or append platform-specific worker flags before the first job.
func NewProcessPoolLauncher(command, dir string) *ProcessPoolLauncher {
	workers := runtime.GOMAXPROCS(0)
	if workers > defaultPoolWorkerCount {
		workers = defaultPoolWorkerCount
	}
	if workers < 1 {
		workers = 1
	}
	return &ProcessPoolLauncher{
		Command:       command,
		Args:          []string{"--task-worker-pool"},
		Dir:           dir,
		MaxWorkers:    workers,
		QueueCapacity: minInt(defaultPoolQueueSize, workers*2),
	}
}

func (l *ProcessPoolLauncher) SetMCPBroker(broker coremcp.Broker) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.Broker = broker
	l.mu.Unlock()
}

func (l *ProcessPoolLauncher) SetDangerousMode(enabled bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	args := make([]string, 0, len(l.Args)+1)
	for _, arg := range l.Args {
		if arg != "--dangerously" && arg != "--yolo" {
			args = append(args, arg)
		}
	}
	if enabled {
		args = append(args, "--dangerously")
	}
	l.Args = args
}

func (l *ProcessPoolLauncher) Start(ctx context.Context, req WorkerRequest) (Process, error) {
	if l == nil {
		return nil, errors.New("task process pool launcher is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.startOnce.Do(func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.closed {
			return
		}
		capacity := l.QueueCapacity
		if capacity <= 0 {
			capacity = defaultPoolQueueSize
		}
		maxWorkers := l.MaxWorkers
		if maxWorkers <= 0 {
			maxWorkers = 1
		}
		// Keep room for jobs that are already being handed to workers. The
		// configured queue capacity describes waiting jobs, not the scheduler's
		// handoff window.
		capacity += maxWorkers
		l.submit = make(chan *poolJob, capacity)
		l.slots = make(chan struct{}, capacity)
		l.shutdown = make(chan struct{})
		l.wake = make(chan struct{}, 1)
		l.poolCtx, l.poolCancel = context.WithCancelCause(context.Background())
		l.group = conc.NewWaitGroup()
		l.workerGroup = conc.NewWaitGroup()
		l.group.Go(l.runScheduler)
	})

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, errors.New("task process pool is shut down")
	}
	submit := l.submit
	shutdown := l.shutdown
	l.mu.Unlock()
	if submit == nil || shutdown == nil {
		return nil, errors.New("task process pool is not initialized")
	}

	jobCtx, cancel := context.WithCancelCause(ctx)
	job := &poolJob{
		ctx:    jobCtx,
		cancel: cancel,
		req:    req,
		result: make(chan workerDone, 1),
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		cancel(errors.New("task process pool is shut down"))
		return nil, errors.New("task process pool is shut down")
	}
	select {
	case submit <- job:
		return &poolJobProcess{job: job}, nil
	case <-ctx.Done():
		cancel(context.Cause(ctx))
		return nil, ctx.Err()
	case <-shutdown:
		cancel(errors.New("task process pool is shut down"))
		return nil, errors.New("task process pool is shut down")
	default:
		cancel(errors.New("task process pool queue is full"))
		return nil, fmt.Errorf("task process pool queue is full")
	}
}

// Close stops the scheduler and all idle or active worker processes. It is
// safe to call more than once and also safe to call before the first Start.
func (l *ProcessPoolLauncher) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		if l.poolCancel != nil {
			l.poolCancel(errors.New("task process pool closed"))
		}
		if l.shutdown != nil {
			close(l.shutdown)
		}
		group := l.group
		workerGroup := l.workerGroup
		l.mu.Unlock()
		if group != nil {
			if recovered := group.WaitAndRecover(); recovered != nil {
				l.mu.Lock()
				l.closeErr = recovered.AsError()
				l.mu.Unlock()
			}
		}
		if workerGroup != nil {
			if recovered := workerGroup.WaitAndRecover(); recovered != nil {
				l.mu.Lock()
				if l.closeErr == nil {
					l.closeErr = recovered.AsError()
				}
				l.mu.Unlock()
			}
		}
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closeErr
}

func (l *ProcessPoolLauncher) runScheduler() {
	l.mu.Lock()
	maxWorkers := l.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	submit := l.submit
	shutdown := l.shutdown
	poolCtx := l.poolCtx
	workerGroup := l.workerGroup
	l.mu.Unlock()

	state := poolSchedulerState{
		launcher:    l,
		maxWorkers:  maxWorkers,
		complete:    make(chan poolCompletion, maxWorkers),
		workerGroup: workerGroup,
		active:      make(map[*poolWorker]*poolJob),
		ctx:         poolCtx,
	}
	for {
		state.dispatch()
		select {
		case job := <-submit:
			if job == nil {
				return
			}
			state.queue = append(state.queue, job)
		case completion := <-state.complete:
			delete(state.active, completion.worker)
			if completion.worker != nil && completion.healthy {
				state.idle = append(state.idle, completion.worker)
			} else if completion.worker != nil {
				if state.workers > 0 {
					state.workers--
				}
				completion.worker.stop()
			}
			completion.job.cancel(nil)
			completion.job.deliver(workerDone{result: completion.result, err: completion.err})
		case <-shutdown:
			// Start 的 send 与 Close 的 closed=true 由同一把锁串行化，
			// 因此收到 shutdown 后不再有新的 job 进入 submit；但已 send
			// 成功、尚未被 scheduler 取走的 job 可能滞留在通道里，
			// 必须在这里收走并投递取消结果，否则 Wait 会永久阻塞。
			state.drainSubmitted(submit)
			state.stopAll()
			return
		}
	}
}

type poolSchedulerState struct {
	launcher    *ProcessPoolLauncher
	maxWorkers  int
	workers     int
	queue       []*poolJob
	idle        []*poolWorker
	complete    chan poolCompletion
	workerGroup *conc.WaitGroup
	active      map[*poolWorker]*poolJob
	ctx         context.Context
}

func (s *poolSchedulerState) dispatch() {
	for len(s.queue) > 0 {
		job := s.queue[0]
		if err := job.ctx.Err(); err != nil {
			s.queue = s.queue[1:]
			cause := context.Cause(job.ctx)
			job.deliver(workerDone{result: canceledWorkerResult(job.req, cause), err: cause})
			continue
		}

		var worker *poolWorker
		if len(s.idle) > 0 {
			worker, s.idle = popIdleWorker(s.idle, randv2.IntN(len(s.idle)))
		} else if s.workers < s.maxWorkers {
			var err error
			worker, err = s.launcher.newPoolWorker()
			if err != nil || worker == nil {
				s.queue = s.queue[1:]
				if err == nil {
					err = errors.New("start task pool worker failed")
				}
				job.deliver(workerDone{result: WorkerResult{
					TaskID:    job.req.TaskID,
					SessionID: job.req.SessionID,
					Error:     err.Error(),
					ExitCode:  1,
				}, err: err})
				continue
			}
			s.workers++
		} else {
			return
		}

		s.queue = s.queue[1:]
		s.active[worker] = job
		job.setWorker(worker)
		run := func() {
			result, err, healthy := runPoolWorkerSafely(worker, job)
			s.complete <- poolCompletion{job: job, worker: worker, result: result, err: err, healthy: healthy}
		}
		if s.workerGroup != nil {
			s.workerGroup.Go(run)
		} else {
			go run()
		}
	}
}

func popIdleWorker(idle []*poolWorker, index int) (*poolWorker, []*poolWorker) {
	if index < 0 || index >= len(idle) {
		panic(fmt.Sprintf("idle worker index %d out of range [0,%d)", index, len(idle)))
	}
	worker := idle[index]
	copy(idle[index:], idle[index+1:])
	idle[len(idle)-1] = nil
	return worker, idle[:len(idle)-1]
}

func runPoolWorkerSafely(worker *poolWorker, job *poolJob) (result WorkerResult, err error, healthy bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = WorkerResult{
				TaskID:    job.req.TaskID,
				SessionID: job.req.SessionID,
				Error:     fmt.Sprintf("task pool worker panic: %v\n%s", recovered, debug.Stack()),
				ExitCode:  1,
			}
			err = errors.New(result.Error)
			healthy = false
		}
	}()
	return worker.run(job)
}

func (s *poolSchedulerState) drainSubmitted(submit <-chan *poolJob) {
	for {
		select {
		case job := <-submit:
			if job == nil {
				return
			}
			job.cancel(errors.New("task process pool closed"))
			job.deliver(workerDone{result: canceledWorkerResult(job.req, context.Cause(job.ctx)), err: context.Cause(job.ctx)})
		default:
			return
		}
	}
}

func (s *poolSchedulerState) stopAll() {
	for _, worker := range s.idle {
		worker.stop()
	}
	for worker, job := range s.active {
		job.cancel(errors.New("task process pool closed"))
		worker.stop()
		cause := context.Cause(job.ctx)
		job.deliver(workerDone{result: canceledWorkerResult(job.req, cause), err: cause})
	}
	for _, job := range s.queue {
		job.cancel(errors.New("task process pool closed"))
		cause := context.Cause(job.ctx)
		job.deliver(workerDone{result: canceledWorkerResult(job.req, cause), err: cause})
	}
	s.active = nil
	s.idle = nil
	s.queue = nil
}

type poolCompletion struct {
	job     *poolJob
	worker  *poolWorker
	result  WorkerResult
	err     error
	healthy bool
}

type poolJob struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	req    WorkerRequest
	result chan workerDone

	mu      sync.RWMutex
	worker  *poolWorker
	partial WorkerResult

	deliverOnce sync.Once
}

func (j *poolJob) deliver(done workerDone) {
	if j == nil {
		return
	}
	j.deliverOnce.Do(func() {
		j.result <- done
	})
}

func (j *poolJob) setWorker(worker *poolWorker) {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.worker = worker
	j.mu.Unlock()
}

func (j *poolJob) getWorker() *poolWorker {
	if j == nil {
		return nil
	}
	j.mu.RLock()
	worker := j.worker
	j.mu.RUnlock()
	return worker
}

func (j *poolJob) recordEvent(event WorkerStreamEvent) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.partial.TaskID = j.req.TaskID
	j.partial.SessionID = j.req.SessionID
	if event.Delta != "" {
		j.partial.Content += event.Delta
	}
	if event.Error != "" {
		j.partial.Error = event.Error
	}
	if event.Usage != nil {
		usage := tokentracer.UsageFromModelUsage(*event.Usage)
		if !usage.Empty() {
			if j.partial.Usage == nil {
				j.partial.Usage = &usage
			} else {
				merged := j.partial.Usage.Add(usage)
				j.partial.Usage = &merged
			}
			j.partial.UsedTokens = usageTokenTotal(*j.partial.Usage)
		}
	}
}

func (j *poolJob) partialResult() WorkerResult {
	if j == nil {
		return WorkerResult{}
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	result := j.partial
	if result.Usage != nil {
		usage := *result.Usage
		result.Usage = &usage
	}
	return result
}

func mergeWorkerResultWithPartial(result, partial WorkerResult) WorkerResult {
	if result.TaskID == "" {
		result.TaskID = partial.TaskID
	}
	if result.SessionID == "" {
		result.SessionID = partial.SessionID
	}
	if result.Content == "" {
		result.Content = partial.Content
	}
	if result.Error == "" {
		result.Error = partial.Error
	}
	if result.UsedTokens == 0 {
		result.UsedTokens = partial.UsedTokens
	}
	if result.Usage == nil && partial.Usage != nil {
		usage := *partial.Usage
		result.Usage = &usage
	}
	return result
}

func (j *poolJob) pid() int {
	if j == nil {
		return 0
	}
	j.mu.RLock()
	worker := j.worker
	j.mu.RUnlock()
	if worker == nil || worker.cmd == nil || worker.cmd.Process == nil {
		return 0
	}
	return worker.cmd.Process.Pid
}

type poolJobProcess struct {
	job *poolJob

	waitOnce sync.Once
	mu       sync.RWMutex
	result   WorkerResult
	err      error
}

func (p *poolJobProcess) PID() int {
	if p == nil || p.job == nil {
		return 0
	}
	return p.job.pid()
}

func (p *poolJobProcess) Wait() (WorkerResult, error) {
	if p == nil || p.job == nil {
		return WorkerResult{ExitCode: 1}, errors.New("task pool job is nil")
	}
	p.waitOnce.Do(func() {
		done := <-p.job.result
		p.mu.Lock()
		p.result = done.result
		p.err = done.err
		p.mu.Unlock()
	})
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.result, p.err
}

func (p *poolJobProcess) Stop() error {
	return p.StopWithCause(context.Canceled)
}

func (p *poolJobProcess) StopWithCause(cause error) error {
	if p == nil || p.job == nil {
		return nil
	}
	if cause == nil {
		cause = context.Canceled
	}
	p.job.cancel(cause)
	return nil
}

func (p *poolJobProcess) PartialResult() WorkerResult {
	if p == nil || p.job == nil {
		return WorkerResult{}
	}
	return p.job.partialResult()
}

// UpdateMCPSnapshot 在任务执行期间推送更新的 MCP 快照给承载该任务的 worker。
// 队列中的任务（尚未绑定 worker）则忽略更新——任务启动时已带上当前快照。
func (p *poolJobProcess) UpdateMCPSnapshot(snapshot coremcp.Snapshot) error {
	if p == nil || p.job == nil {
		return nil
	}
	worker := p.job.getWorker()
	if worker == nil {
		return nil
	}
	return worker.send(workerMessage{Protocol: workerProtocolV2, Type: workerMessageSnapshot, Snapshot: snapshot.Clone()})
}

func canceledWorkerResult(req WorkerRequest, err error) WorkerResult {
	if err == nil {
		err = context.Canceled
	}
	return WorkerResult{TaskID: req.TaskID, SessionID: req.SessionID, Error: err.Error(), ExitCode: -1}
}

type poolWorker struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	broker coremcp.Broker
	ctx    context.Context
	cancel context.CancelFunc

	wall time.Duration

	// roleName/roleColor 是该 worker 的具名角色（建池时从角色池分配）。
	roleName  string
	roleColor string

	writeMu    sync.Mutex
	mu         sync.Mutex
	stderr     limitedTailBuffer
	stderrDone chan struct{}
	currentCtx context.Context
	currentJob *poolJob
	current    chan workerDone
	ready      chan error
	closed     chan struct{}
	stopOnce   sync.Once
}

func randomPersonaOrder(count int) []int {
	if count <= 0 {
		return nil
	}
	return randv2.Perm(count)
}

// nextWorkerRoleLocked 在已持有 l.mu 的前提下按随机排列分配角色；一轮内不重复。
func (l *ProcessPoolLauncher) nextWorkerRoleLocked() persona {
	if l == nil || len(defaultPersonas) == 0 {
		return persona{}
	}
	if len(l.roleOrder) != len(defaultPersonas) || l.roleCursor >= len(l.roleOrder) {
		l.roleOrder = randomPersonaOrder(len(defaultPersonas))
		l.roleCursor = 0
	}
	role := defaultPersonas[l.roleOrder[l.roleCursor]]
	l.roleCursor++
	return role
}

// WorkerRole 返回承载该任务的池 worker 的角色名/色。任务尚未绑定 worker
// （排队中）时短暂等待绑定；超时返回空，调用方回落进程内角色分配。
func (p *poolJobProcess) WorkerRole() (name, color string) {
	if p == nil || p.job == nil {
		return "", ""
	}
	deadline := time.Now().Add(time.Second)
	for {
		if worker := p.job.getWorker(); worker != nil {
			return worker.roleName, worker.roleColor
		}
		if time.Now().After(deadline) {
			return "", ""
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (l *ProcessPoolLauncher) newPoolWorker() (*poolWorker, error) {
	l.mu.Lock()
	command := strings.TrimSpace(l.Command)
	args := append([]string(nil), l.Args...)
	env := append([]string(nil), l.Env...)
	dir := l.Dir
	broker := l.Broker
	wall := l.JobWallTime
	role := l.nextWorkerRoleLocked()
	l.mu.Unlock()
	if command == "" {
		return nil, errors.New("task worker command is empty")
	}
	if role.Name == "" {
		role = persona{Name: "task", Color: "#888888"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		cancel()
		return nil, err
	}
	worker := &poolWorker{
		cmd: cmd, stdin: stdin, broker: broker, ctx: ctx, cancel: cancel,
		wall: wall, roleName: role.Name, roleColor: role.Color,
		ready: make(chan error, 1), closed: make(chan struct{}), stderrDone: make(chan struct{}),
	}
	go func() {
		defer close(worker.stderrDone)
		_, _ = io.Copy(&worker.stderr, stderr)
		_ = stderr.Close()
	}()
	go worker.readLoop(stdout)
	go func() {
		waitErr := cmd.Wait()
		worker.failCurrent(worker.exitError(waitErr))
		worker.stopOnce.Do(func() { close(worker.closed) })
	}()
	if err := worker.send(workerMessage{Protocol: workerProtocolV2, Type: workerMessageHello}); err != nil {
		worker.stop()
		return nil, err
	}
	readyCtx, readyCancel := context.WithTimeout(context.Background(), poolReadyTimeout)
	defer readyCancel()
	select {
	case err := <-worker.ready:
		if err != nil {
			worker.stop()
			return nil, err
		}
	case <-readyCtx.Done():
		worker.stop()
		return nil, fmt.Errorf("task pool worker ready timeout: %w", readyCtx.Err())
	}
	return worker, nil
}

func (w *poolWorker) readLoop(stdout io.Reader) {
	scanner := newJSONLineScanner(stdout)
	for scanner.Scan() {
		var msg workerMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			w.failCurrent(fmt.Errorf("decode task pool worker message: %w", err))
			return
		}
		switch msg.Type {
		case workerMessageReady:
			select {
			case w.ready <- nil:
			default:
			}
		case workerMessageResult:
			w.mu.Lock()
			current := w.current
			w.current = nil
			w.currentCtx = nil
			w.currentJob = nil
			w.mu.Unlock()
			if current != nil {
				current <- workerDone{result: msg.Result()}
			}
		case workerMessageMCPCall:
			go w.handleMCPCall(msg)
		case workerMessageEvent:
			w.mu.Lock()
			job := w.currentJob
			w.mu.Unlock()
			if job != nil && msg.Event != nil {
				job.recordEvent(*msg.Event)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		w.failCurrent(err)
	}
}

func (w *poolWorker) run(job *poolJob) (WorkerResult, error, bool) {
	if err := job.ctx.Err(); err != nil {
		cause := context.Cause(job.ctx)
		return canceledWorkerResult(job.req, cause), cause, true
	}
	resultCh := make(chan workerDone, 1)
	w.mu.Lock()
	w.current = resultCh
	w.currentCtx = job.ctx
	w.currentJob = job
	w.mu.Unlock()
	start := NewWorkerStartMessage(job.req, job.req.MCPSnapshot)
	start.Protocol = workerProtocolV2
	if err := w.send(start); err != nil {
		w.failCurrent(err)
		return WorkerResult{TaskID: job.req.TaskID, SessionID: job.req.SessionID, Error: err.Error(), ExitCode: 1}, err, false
	}
	jobCtx := job.ctx
	if w.wall > 0 {
		var stopWall context.CancelFunc
		jobCtx, stopWall = context.WithTimeout(job.ctx, w.wall)
		defer stopWall()
	}
	select {
	case done := <-resultCh:
		result := mergeWorkerResultWithPartial(done.result, job.partialResult())
		protocolHealthy := done.err == nil
		err := done.err
		if err == nil && result.Error != "" {
			err = errors.New(result.Error)
		}
		// 收到结构合法的 worker.result 说明进程协议仍健康；任务本身失败
		// 不应销毁可复用 worker，否则短暂 provider 故障会引发进程抖动。
		return result, err, protocolHealthy
	case <-jobCtx.Done():
		if err := job.ctx.Err(); err != nil {
			// 调用方取消：保留带来源的 cause，并回收当前 worker。
			_ = w.send(workerMessage{Protocol: workerProtocolV2, Type: workerMessageCancel, TaskID: job.req.TaskID})
			w.stop()
			cause := context.Cause(job.ctx)
			result := mergeWorkerResultWithPartial(canceledWorkerResult(job.req, cause), job.partialResult())
			return result, cause, false
		}
		// 墙钟超时：不信任 worker 后续，直接拉起失败并回收该 worker。
		err := fmt.Errorf("task job wall clock exceeded %s", w.wall)
		w.stop()
		result := mergeWorkerResultWithPartial(WorkerResult{TaskID: job.req.TaskID, SessionID: job.req.SessionID, Error: err.Error(), ExitCode: 1}, job.partialResult())
		return result, err, false
	case <-w.closed:
		err := w.exitError(nil)
		result := mergeWorkerResultWithPartial(WorkerResult{TaskID: job.req.TaskID, SessionID: job.req.SessionID, Error: err.Error(), ExitCode: 1}, job.partialResult())
		return result, err, false
	}
}

func (w *poolWorker) handleMCPCall(msg workerMessage) {
	w.mu.Lock()
	ctx := w.currentCtx
	w.mu.Unlock()
	if ctx == nil {
		ctx = w.ctx
	}
	response := workerMessage{Protocol: workerProtocolV2, Type: workerMessageMCPResult, RequestID: msg.RequestID, TaskID: msg.TaskID}
	if w.broker == nil {
		response.Error = "MCP broker is unavailable"
	} else {
		content, err := w.broker.Call(ctx, msg.Tool, msg.Input)
		response.Content = content
		if err != nil {
			response.Error = err.Error()
		}
	}
	_ = w.send(response)
}

func (w *poolWorker) exitError(waitErr error) error {
	if w.stderrDone != nil {
		<-w.stderrDone
	}
	stderr := strings.TrimSpace(w.stderr.String())
	if stderr != "" {
		if waitErr != nil {
			return fmt.Errorf("task pool worker exited: %w; stderr: %s", waitErr, stderr)
		}
		return fmt.Errorf("task pool worker exited; stderr: %s", stderr)
	}
	if waitErr != nil {
		return fmt.Errorf("task pool worker exited: %w", waitErr)
	}
	return errors.New("task pool worker exited")
}

func (w *poolWorker) failCurrent(err error) {
	if err == nil {
		err = errors.New("task pool worker failed")
	}
	w.mu.Lock()
	current := w.current
	w.current = nil
	w.currentCtx = nil
	w.currentJob = nil
	w.mu.Unlock()
	if current != nil {
		current <- workerDone{result: WorkerResult{Error: err.Error(), ExitCode: 1}, err: err}
	}
	select {
	case w.ready <- err:
	default:
	}
}

func (w *poolWorker) send(msg workerMessage) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if w.stdin == nil {
		return errors.New("task pool worker stdin is closed")
	}
	return json.NewEncoder(w.stdin).Encode(msg)
}

func (w *poolWorker) stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		if w.cancel != nil {
			w.cancel()
		}
		if w.stdin != nil {
			_ = w.stdin.Close()
		}
		if w.cmd != nil && w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
		close(w.closed)
	})
}

// jsonLineScanner adds a bounded frame size to the worker JSONL protocol.
type limitedTailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *limitedTailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > poolStderrLimit {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-poolStderrLimit:]...)
	}
	return len(p), nil
}

func (b *limitedTailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(append([]byte(nil), b.buf...))
}

type jsonLineScanner struct {
	scanner *bufio.Scanner
}

func newJSONLineScanner(reader io.Reader) *jsonLineScanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	return &jsonLineScanner{scanner: scanner}
}

func (s *jsonLineScanner) Scan() bool    { return s.scanner.Scan() }
func (s *jsonLineScanner) Bytes() []byte { return s.scanner.Bytes() }
func (s *jsonLineScanner) Err() error    { return s.scanner.Err() }

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
