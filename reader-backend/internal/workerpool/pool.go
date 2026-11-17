package workerpool

import (
	"context"
	"log"
	"runtime/debug"
	"sync"
)

type Job func()

type Pool struct {
	jobQueue chan Job
	wg       sync.WaitGroup
	once     sync.Once
	mu       sync.RWMutex
	closed   bool
	done     chan struct{}
}

var GlobalPool *Pool

// InitPool initializes a global worker pool with worker count and buffer capacity.
func InitPool(workers int, queueSize int) {
	if GlobalPool != nil {
		return
	}
	GlobalPool = &Pool{
		jobQueue: make(chan Job, queueSize), done: make(chan struct{}),
	}
	for i := 0; i < workers; i++ {
		GlobalPool.wg.Add(1)
		go func(workerID int) {
			defer GlobalPool.wg.Done()
			for job := range GlobalPool.jobQueue {
				safelyExecute(job)
			}
		}(i)
	}
	log.Printf("[WorkerPool] Initialized with %d workers and buffer capacity %d", workers, queueSize)
}

// Submit submits a job to the worker pool. Returns false if the queue is full.
func Submit(job Job) bool {
	if GlobalPool == nil || GlobalPool.jobQueue == nil {
		return false
	}

	GlobalPool.mu.RLock()
	defer GlobalPool.mu.RUnlock()
	if GlobalPool.closed {
		return false
	}
	select {
	case GlobalPool.jobQueue <- job:
		return true
	default:
		log.Println("[WorkerPool Warning] Job queue is full, dropping async task")
		return false
	}
}

// Shutdown gracefully waits for enqueued jobs to finish and closes worker pool.
func Shutdown() { _ = ShutdownContext(context.Background()) }

func ShutdownContext(ctx context.Context) error {
	if GlobalPool == nil {
		return nil
	}
	p := GlobalPool
	p.once.Do(func() {
		p.mu.Lock()
		p.closed = true
		close(p.jobQueue)
		p.mu.Unlock()
		go func() { p.wg.Wait(); close(p.done) }()
	})
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func safelyExecute(job Job) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[WorkerPool Panic Recovered] %v\nStack: %s", r, string(debug.Stack()))
		}
	}()
	job()
}
