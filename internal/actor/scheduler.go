package actor

import (
	"hash/fnv"
	"sync"
)

// scheduler 实现分片单写者调度（spec §7）：hash(ActorID) → N 分片，
// 每分片一个 worker goroutine 串行消费任务 ⇒ 同一 actor 至多被一个
// worker 处理（I1 由构造保证）。任务粒度是“处理一条投递”。
type scheduler struct {
	shards []*shard
	quit   chan struct{}
	once   sync.Once
}

type shard struct {
	mu       sync.Mutex
	cond     *sync.Cond
	queue    []func()
	draining int // 正在执行的任务数
}

func newScheduler(shards int) *scheduler {
	if shards <= 0 {
		shards = 1
	}
	s := &scheduler{quit: make(chan struct{})}
	for i := 0; i < shards; i++ {
		sh := &shard{}
		sh.cond = sync.NewCond(&sh.mu)
		s.shards = append(s.shards, sh)
		go sh.loop(s.quit)
	}
	return s
}

// submit 派发任务到 id 所属分片。
func (s *scheduler) submit(id ActorID, task func()) {
	sh := s.shards[int(shardHash(id))%len(s.shards)]
	sh.enqueue(task)
}

// drainBarrier 返回一个通道：所有分片排空当前+已入队任务后关闭。
// 用于确定性测试（Drain）与 Stop。
func (s *scheduler) drainBarrier() <-chan struct{} {
	done := make(chan struct{})
	var wg sync.WaitGroup
	for _, sh := range s.shards {
		wg.Add(1)
		sh.enqueue(func() { wg.Done() })
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	return done
}

func (sh *shard) enqueue(task func()) {
	sh.mu.Lock()
	sh.queue = append(sh.queue, task)
	sh.cond.Signal()
	sh.mu.Unlock()
}

func (sh *shard) loop(quit <-chan struct{}) {
	for {
		sh.mu.Lock()
		for len(sh.queue) == 0 {
			sh.cond.Wait()
			select {
			case <-quit:
				sh.mu.Unlock()
				return
			default:
			}
		}
		task := sh.queue[0]
		sh.queue = sh.queue[1:]
		sh.draining++
		sh.mu.Unlock()

		task()

		sh.mu.Lock()
		sh.draining--
		if len(sh.queue) == 0 && sh.draining == 0 {
			sh.cond.Broadcast()
		}
		sh.mu.Unlock()
	}
}

// quiescentNow 采样所有分片是否空闲（无排队且无在执行）。
func (s *scheduler) quiescentNow() bool {
	for _, sh := range s.shards {
		sh.mu.Lock()
		idle := len(sh.queue) == 0 && sh.draining == 0
		sh.mu.Unlock()
		if !idle {
			return false
		}
	}
	return true
}

func (s *scheduler) stop() {
	s.once.Do(func() {
		close(s.quit)
		for _, sh := range s.shards {
			sh.mu.Lock()
			sh.cond.Broadcast()
			sh.mu.Unlock()
		}
	})
}

func shardHash(id ActorID) uint32 {
	h := fnv.New32a()
	h.Write([]byte(id.Type))
	h.Write([]byte{0})
	h.Write([]byte(id.Key))
	return h.Sum32()
}
