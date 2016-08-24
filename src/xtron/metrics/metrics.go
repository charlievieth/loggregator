package metrics

import (
	"sync"
	"sync/atomic"
)

type batch struct {
	name  string
	tags  map[string]string
	value *uint64
}

type Batcher struct {
	simple map[string]batch
	tagged map[string][]batch
}

type simpleBatcher struct {
	m  map[string]*uint64
	mu sync.RWMutex
}

func (b *simpleBatcher) Increment(name string, delta uint64) {
	// Fast path
	b.mu.RLock()
	if v, ok := b.m[name]; ok {
		atomic.AddUint64(v, delta)
		b.mu.RUnlock()
		return
	}
	b.mu.RUnlock()

	// Write-Lock and recheck condition
	b.mu.Lock()
	if v, ok := b.m[name]; ok {
		atomic.AddUint64(v, delta)
	} else {
		b.m[name] = (*uint64)(&delta)
	}
	b.mu.Unlock()
}

type taggedBatcher struct {
	m  map[string][]batch
	mu sync.RWMutex
}
