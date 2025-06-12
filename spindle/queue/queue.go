package queue

type Job struct {
	Run    func() error
	OnFail func(error)
}

type Queue struct {
	jobs chan Job
}

func NewQueue(size int) *Queue {
	return &Queue{
		jobs: make(chan Job, size),
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

func (q *Queue) StartRunner() {
	go func() {
		for job := range q.jobs {
			if err := job.Run(); err != nil {
				if job.OnFail != nil {
					job.OnFail(err)
				}
			}
		}
	}()
}
