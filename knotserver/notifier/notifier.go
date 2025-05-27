package notifier

import (
	"sync"
)

type Notifier struct {
	subscribers map[chan struct{}]struct{}
	mu          sync.Mutex
}

func New() Notifier {
	return Notifier{
		subscribers: make(map[chan struct{}]struct{}),
	}
}

func (n *Notifier) Subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	n.subscribers[ch] = struct{}{}
	n.mu.Unlock()
	return ch
}

func (n *Notifier) Unsubscribe(ch chan struct{}) {
	n.mu.Lock()
	delete(n.subscribers, ch)
	close(ch)
	n.mu.Unlock()
}

func (n *Notifier) NotifyAll() {
	n.mu.Lock()
	for ch := range n.subscribers {
		select {
		case ch <- struct{}{}:
		default:
			// avoid blocking if channel is full
		}
	}
	n.mu.Unlock()
}
