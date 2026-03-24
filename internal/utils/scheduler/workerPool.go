package scheduler

import (
	"sync"
	
	"wfts/internal/model"
)

type WorkerPool struct {
	bfShutdown 	func([]any)
	buf 		chan struct{}
	quit      	chan struct{}
	collector 	chan any
	heap		*minMaxHeap
	wg        	*sync.WaitGroup
	mu 			*sync.Mutex
}

func NewWorkerPool(beforeShutdown func([]any), size, queueCapacity int) *WorkerPool {
	wp := &WorkerPool{
		bfShutdown: 	beforeShutdown,
		buf: 			make(chan struct{}, queueCapacity),
		quit:      		make(chan struct{}),
		collector: 		make(chan any, queueCapacity),
		heap: 			NewMinMaxHeap(),
		wg:        		new(sync.WaitGroup),
		mu:				new(sync.Mutex),
	}
	for range size {
		go wp.worker()
	}
	return wp
}

func (wp *WorkerPool) Submit(task *model.CrawlNode) {
	orig := task.Activation
	task.Activation = func() model.CompletionState {
		defer wp.wg.Done()
		return orig()
	}
	
	wp.mu.Lock()
	select {
	case wp.buf <- struct{}{}:
		wp.wg.Add(1)
		wp.heap.Insert(task)
		wp.mu.Unlock()

	default:
		if worstTask, exist := wp.heap.GetMin(); exist && task.Priority > worstTask.Value.Priority {
			wp.heap.DeleteMin()
			wp.heap.Insert(task)
		}
		wp.mu.Unlock()
	}
}

func (wp *WorkerPool) worker() {
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
				if task.Value.Activation() == model.Canceled {
					select {
					case wp.collector <- task.Value.CrawlToken:
					case <-wp.collector:
						select {
							case wp.collector <- task.Value.CrawlToken: // - можно гарантировать добавление конкретного элемента в буффер? - можно, а зачем?
							default:

						}

					}
				}
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

func (wp *WorkerPool) Backup() []any {
	canceleds := wp.heap.tokens()
	for {
		select {
		case token, ok := <-wp.collector:
			if !ok {
				return canceleds
			}
			canceleds = append(canceleds, token)

		default:
			return canceleds

		}
	}
}

func (wp *WorkerPool) Wait() {
	wp.wg.Wait()
}

func (wp *WorkerPool) Stop() {
	defer close(wp.collector)
	defer func () {
		wp.bfShutdown(wp.Backup())
	}()
	close(wp.quit)
	close(wp.buf)
	wp.Wait()
}