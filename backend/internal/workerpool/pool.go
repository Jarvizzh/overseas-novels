package workerpool

import (
	"log"
	"runtime/debug"
	"sync"
)

type Job func()

type Pool struct {
	jobQueue chan Job
	wg       sync.WaitGroup
	once     sync.Once
}

var GlobalPool *Pool

// InitPool initializes a global worker pool with worker count and buffer capacity.
func InitPool(workers int, queueSize int) {
	if GlobalPool != nil {
		return
	}
	GlobalPool = &Pool{
		jobQueue: make(chan Job, queueSize),
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
		// Fallback if pool is not initialized
		go safelyExecute(job)
		return true
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
func Shutdown() {
	if GlobalPool != nil {
		GlobalPool.once.Do(func() {
			close(GlobalPool.jobQueue)
			GlobalPool.wg.Wait()
			log.Println("[WorkerPool] Shutdown complete")
		})
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
