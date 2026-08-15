package subagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	coremcp "paw/internal/mcp"

	"github.com/sourcegraph/conc"
)

const (
	defaultPoolWorkerCount = 4
	defaultPoolQueueSize   = 8
	poolReadyTimeout       = 30 * time.Second
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

	submit    chan *poolJob
	shutdown  chan struct{}
	wake      chan struct{}
	slots     chan struct{}
	startOnce sync.Once
	closeOnce sync.Once

	group       *conc.WaitGroup
	workerGroup *conc.WaitGroup
	poolCtx     context.Context
	poolCancel  context.CancelFunc
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
		Args:          []string{"--subagent-worker-pool"},
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
		return nil, errors.New("subagent process pool launcher is nil")
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
		l.poolCtx, l.poolCancel = context.WithCancel(context.Background())
		l.group = conc.NewWaitGroup()
		l.workerGroup = conc.NewWaitGroup()
		l.group.Go(l.runScheduler)
	})

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, errors.New("subagent process pool is shut down")
	}
	submit := l.submit
	shutdown := l.shutdown
	l.mu.Unlock()
	if submit == nil || shutdown == nil {
		return nil, errors.New("subagent process pool is not initialized")
	}

	jobCtx, cancel := context.WithCancel(ctx)
	job := &poolJob{
		ctx:    jobCtx,
		cancel: cancel,
		req:    req,
		result: make(chan workerDone, 1),
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		cancel()
		return nil, errors.New("subagent process pool is shut down")
	}
	select {
	case submit <- job:
		return &poolJobProcess{job: job}, nil
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	case <-shutdown:
		cancel()
		return nil, errors.New("subagent process pool is shut down")
	default:
		cancel()
		return nil, fmt.Errorf("subagent process pool queue is full")
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
			l.poolCancel()
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
			if state.workers > 0 {
				state.workers--
			}
			delete(state.active, completion.worker)
			if completion.worker != nil && completion.healthy {
				state.idle = append(state.idle, completion.worker)
			} else if completion.worker != nil {
				completion.worker.stop()
			}
			completion.job.cancel()
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
			job.deliver(workerDone{result: canceledWorkerResult(job.req, err), err: err})
			continue
		}

		var worker *poolWorker
		if len(s.idle) > 0 {
			last := len(s.idle) - 1
			worker = s.idle[last]
			s.idle = s.idle[:last]
		} else if s.workers < s.maxWorkers {
			var err error
			worker, err = s.launcher.newPoolWorker()
			if err != nil || worker == nil {
				s.queue = s.queue[1:]
				if err == nil {
					err = errors.New("start subagent pool worker failed")
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

func runPoolWorkerSafely(worker *poolWorker, job *poolJob) (result WorkerResult, err error, healthy bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = WorkerResult{
				TaskID:    job.req.TaskID,
				SessionID: job.req.SessionID,
				Error:     fmt.Sprintf("subagent pool worker panic: %v\n%s", recovered, debug.Stack()),
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
			job.cancel()
			job.deliver(workerDone{result: canceledWorkerResult(job.req, context.Canceled), err: context.Canceled})
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
		job.cancel()
		worker.stop()
		job.deliver(workerDone{result: canceledWorkerResult(job.req, context.Canceled), err: context.Canceled})
	}
	for _, job := range s.queue {
		job.cancel()
		job.deliver(workerDone{result: canceledWorkerResult(job.req, context.Canceled), err: context.Canceled})
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
	cancel context.CancelFunc
	req    WorkerRequest
	result chan workerDone

	mu     sync.RWMutex
	worker *poolWorker

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
		return WorkerResult{ExitCode: 1}, errors.New("subagent pool job is nil")
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
	if p == nil || p.job == nil {
		return nil
	}
	p.job.cancel()
	return nil
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

	writeMu    sync.Mutex
	mu         sync.Mutex
	currentCtx context.Context
	current    chan workerDone
	ready      chan error
	closed     chan struct{}
	stopOnce   sync.Once
}

func (l *ProcessPoolLauncher) newPoolWorker() (*poolWorker, error) {
	l.mu.Lock()
	command := strings.TrimSpace(l.Command)
	args := append([]string(nil), l.Args...)
	env := append([]string(nil), l.Env...)
	dir := l.Dir
	broker := l.Broker
	l.mu.Unlock()
	if command == "" {
		return nil, errors.New("subagent worker command is empty")
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
		ready: make(chan error, 1), closed: make(chan struct{}),
	}
	go func() {
		_, _ = io.Copy(io.Discard, stderr)
		_ = stderr.Close()
	}()
	go worker.readLoop(stdout)
	go func() {
		_ = cmd.Wait()
		worker.failCurrent(errors.New("subagent pool worker exited"))
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
		return nil, fmt.Errorf("subagent pool worker ready timeout: %w", readyCtx.Err())
	}
	return worker, nil
}

func (w *poolWorker) readLoop(stdout io.Reader) {
	scanner := newJSONLineScanner(stdout)
	for scanner.Scan() {
		var msg workerMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			w.failCurrent(fmt.Errorf("decode subagent pool worker message: %w", err))
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
			w.mu.Unlock()
			if current != nil {
				current <- workerDone{result: msg.Result()}
			}
		case workerMessageMCPCall:
			go w.handleMCPCall(msg)
		case workerMessageEvent:
			// Streaming events are added to the job handle in the next slice.
		}
	}
	if err := scanner.Err(); err != nil {
		w.failCurrent(err)
	} else {
		w.failCurrent(errors.New("subagent pool worker output closed"))
	}
}

func (w *poolWorker) run(job *poolJob) (WorkerResult, error, bool) {
	if err := job.ctx.Err(); err != nil {
		return canceledWorkerResult(job.req, err), err, true
	}
	resultCh := make(chan workerDone, 1)
	w.mu.Lock()
	w.current = resultCh
	w.currentCtx = job.ctx
	w.mu.Unlock()
	start := NewWorkerStartMessage(job.req, job.req.MCPSnapshot)
	start.Protocol = workerProtocolV2
	if err := w.send(start); err != nil {
		w.failCurrent(err)
		return WorkerResult{TaskID: job.req.TaskID, SessionID: job.req.SessionID, Error: err.Error(), ExitCode: 1}, err, false
	}
	select {
	case done := <-resultCh:
		return done.result, done.err, done.err == nil
	case <-job.ctx.Done():
		_ = w.send(workerMessage{Protocol: workerProtocolV2, Type: workerMessageCancel, TaskID: job.req.TaskID})
		w.stop()
		return canceledWorkerResult(job.req, job.ctx.Err()), job.ctx.Err(), false
	case <-w.closed:
		err := errors.New("subagent pool worker exited unexpectedly")
		return WorkerResult{TaskID: job.req.TaskID, SessionID: job.req.SessionID, Error: err.Error(), ExitCode: 1}, err, false
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

func (w *poolWorker) failCurrent(err error) {
	if err == nil {
		err = errors.New("subagent pool worker failed")
	}
	w.mu.Lock()
	current := w.current
	w.current = nil
	w.currentCtx = nil
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
		return errors.New("subagent pool worker stdin is closed")
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
