package queue

import (
	"sync"
)

type Job struct {
	Run    func() error
	OnFail func(error)
}

type Queue struct {
	jobs    chan Job
	workers int
	wg      sync.WaitGroup
}

func NewQueue(queueSize, numWorkers int) *Queue {
	return &Queue{
		jobs:    make(chan Job, queueSize),
		workers: numWorkers,
	}
}

func (q *Queue) Enqueue(job Job) bool {
	select {
	case q.jobs <- job:
		return true
	default:
		return false
	}
}

func (q *Queue) Start() {
	for range q.workers {
		q.wg.Add(1)
		go q.worker()
	}
}

func (q *Queue) worker() {
	defer q.wg.Done()
	for job := range q.jobs {
		if err := job.Run(); err != nil {
			if job.OnFail != nil {
				job.OnFail(err)
			}
		}
	}
}

func (q *Queue) Stop() {
	close(q.jobs)
	q.wg.Wait()
}
