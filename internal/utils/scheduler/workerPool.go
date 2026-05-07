package scheduler

import (
	"sync"
	"time"

	"wfts/internal/model"
)

type WorkerPool struct {
	collection 	Stack
	act 		func(*model.LinkToken) model.CompletionState
	buf 		chan struct{}
	quit      	chan struct{}
	collector 	chan any
	heap		*minMaxHeap
	wg        	*sync.WaitGroup
	mu 			*sync.Mutex
}

func NewWorkerPool(st Stack, activation func(*model.LinkToken) model.CompletionState, size, queueCapacity int) *WorkerPool {
	wp := &WorkerPool{
		collection: 	st,
		act: 			activation,
		buf: 			make(chan struct{}, queueCapacity),
		quit:      		make(chan struct{}),
		heap: 			NewMinMaxHeap(),
		wg:        		new(sync.WaitGroup),
		mu:				new(sync.Mutex),
	}
	for range size {
		go wp.worker()
	}
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				wp.mu.Lock()
				task, err := wp.collection.Pop()
				wp.mu.Unlock()
				if err == nil {
					if token, err := model.DeserializeToken(task); err == nil {
						wp.Submit(token)
					}
				}
			case <-wp.quit:
				return
				
			}
		}
	}()
	return wp
}

func (wp *WorkerPool) heapAct(cn *model.LinkToken) model.CompletionState {
	wp.wg.Done()
	return wp.act(cn)
}

type Stack interface {
	Push([]byte) error
	Pop() ([]byte, error)
	Close() error
}

func (wp *WorkerPool) Submit(task *model.LinkToken) {
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
			if data, err := worstTask.Value.Serialize(); err == nil {
				wp.collection.Push(data)
			}
		} else if exist {
			if data, err := task.Serialize(); err == nil {
				wp.collection.Push(data)
			}
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
				if wp.heapAct(task.Value) == model.Canceled {
					data, err := task.Value.Serialize()
					if err != nil {
						continue
					}
					wp.collection.Push(data)
				}
				continue
			}

			node, err := wp.collection.Pop()
			wp.mu.Unlock()
			if err == nil {
				if token, err := model.DeserializeToken(node); err == nil {
					wp.act(token)
				}
			}

			select {
			case wp.buf <- struct{}{}:
			default:
			}

		case <-wp.quit:
			return

		}
	}
}

func (wp *WorkerPool) backup() {
	for _, token := range wp.heap.tokens() {
		serialized, err := token.Serialize()
		if err != nil {
			continue
		}
		wp.collection.Push(serialized)
	}
}

func (wp *WorkerPool) Wait() {
	wp.wg.Wait()
}

func (wp *WorkerPool) Stop() {
	close(wp.quit)
	close(wp.buf)
	wp.Wait()
	wp.backup()
	wp.collection.Close()
}