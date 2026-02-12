package scheduler

import (
	"sync"
	"sync/atomic"
	
	"wfts/internal/model"
)

type WorkerPool struct {
	buf 		chan struct{}
	quit      	chan struct{}
	heap		*minMaxHeap
	wg        	*sync.WaitGroup
	mu 			*sync.Mutex
	workers   	int32
}

func NewWorkerPool(size, queueCapacity int) *WorkerPool {
	wp := &WorkerPool{
		buf: 			make(chan struct{}, queueCapacity),
		quit:      		make(chan struct{}),
		heap: 			NewMinMaxHeap(),
		wg:        		new(sync.WaitGroup),
		mu:				new(sync.Mutex),
	}
	for range size {
		go wp.worker()
	}
	return wp
}

func (wp *WorkerPool) Submit(task model.CrawlNode) {
	orig := task.Activation
	task.Activation = func() {
		defer wp.wg.Done()
		orig()
	}
	
	wp.mu.Lock()
	select {
	case wp.buf <- struct{}{}:
		wp.wg.Add(1)
		wp.heap.Insert(&task)
		wp.mu.Unlock()

	default:
		if worstTask, exist := wp.heap.GetMin(); exist && task.Priority > worstTask.Value.Priority {
			wp.heap.DeleteMin()
			wp.heap.Insert(&task)
		}
		wp.mu.Unlock()
	}
}

func (wp *WorkerPool) worker() {
	atomic.AddInt32(&wp.workers, 1)
	defer atomic.AddInt32(&wp.workers, -1)
	for {
		select {
		case _, ok := <-wp.buf:
			if !ok {
				return
			}
			wp.mu.Lock()
			task, exist := wp.heap.GetMax()
			if exist {
				wp.heap.DeleteMax()
				wp.mu.Unlock()
				task.Value.Activation()
				continue
			}
			wp.mu.Unlock()

			select {
			case wp.buf <- struct{}{}:
			default:
			}

		case <-wp.quit:
			return
		}
	}
}

func (wp *WorkerPool) Wait() {
	wp.wg.Wait()
}

func (wp *WorkerPool) Stop() {
	close(wp.quit)
	close(wp.buf)
	wp.Wait()
}