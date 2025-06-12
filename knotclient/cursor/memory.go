package cursor

import (
	"sync"
)

type MemoryStore struct {
	store sync.Map
}

func (m *MemoryStore) Set(knot string, cursor int64) {
	m.store.Store(knot, cursor)
}

func (m *MemoryStore) Get(knot string) (cursor int64) {
	if result, ok := m.store.Load(knot); ok {
		if val, ok := result.(int64); ok {
			return val
		}
	}

	return 0
}
