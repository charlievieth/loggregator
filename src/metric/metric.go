package metric

// TODO (immediate):
//   - Atomically access pointers
//   - Use a single 'counter' type, to manage 'value' and 'set' state
//     * This should simplify the 'atomic pointer access' issue
//     * Currently, values are shared, but not the 'set' state
//   - Flushing, start marshaling to whatever bullshit protobufs
//     dropsonde uses
//     * Can these be slimmed down?

// TODO (future):
//   - Cache eviction, the current implementation keeps a record
//     of each *Counter seen, I doubt this will be an issue, but
//     simple LRU cache could be used.

import (
	"errors"
	"sync"
	"sync/atomic"
	"unsafe"
)

type Batch struct {
	name map[string]*Counter
	tags map[string][]*TaggedCounter
	mu   sync.RWMutex
}

func (b *Batch) flush() int64 {
	var n int64
	b.mu.RLock()
	for _, c := range b.name {
		n += c.load().flush()
	}
	b.mu.RUnlock()
	return n
}

func (b *Batch) counters() []*Counter {
	b.mu.RLock()
	list := make([]*Counter, 0, len(b.name))
	for _, c := range b.name {
		list = append(list, c)
	}
	b.mu.RUnlock()
	return list
}

func (b *Batch) Counter(name string) *Counter {
	if c, ok := b.getName(name); ok {
		return c
	}
	return b.addName(name)
}

func (b *Batch) addName(name string) *Counter {
	b.mu.Lock()
	if b.name == nil {
		b.name = make(map[string]*Counter)
	}
	var c *Counter
	if c = b.name[name]; c == nil {
		c = newCounter(name, 0)
		b.name[name] = c
	}
	b.mu.Unlock()
	return c
}

func (b *Batch) getName(name string) (c *Counter, ok bool) {
	b.mu.RLock()
	if b.name != nil {
		c, ok = b.name[name]
	}
	b.mu.RUnlock()
	return c, ok
}

func (b *Batch) lazyInitTags() {
	b.mu.Lock()
	if b.tags == nil {
		b.tags = make(map[string][]*TaggedCounter)
	}
	b.mu.Unlock()
}

// findTag returns the TaggedCounter matching c, if any.  A Read/Write lock
// must be held before calling.
func (b *Batch) findTag(c *TaggedCounter) (*TaggedCounter, bool) {
	if b.tags != nil {
		tags := b.tags[c.Name()]
		for _, cc := range tags {
			if matchTags(c.tags, cc.tags) {
				return cc, true
			}
		}
	}
	return nil, false
}

// getTag returns the TaggedCounter matching c, if any.  The TaggedCounter
// argument c must not have been added to the tags map before calling.
func (b *Batch) getTag(c *TaggedCounter) (*TaggedCounter, bool) {
	b.mu.RLock()
	c, ok := b.findTag(c)
	b.mu.RUnlock()
	return c, ok
}

func (b *Batch) addTag(c *TaggedCounter) *TaggedCounter {
	b.mu.Lock()
	if cc, ok := b.findTag(c); ok {
		b.mu.Unlock()
		return cc
	}
	if b.tags == nil {
		b.tags = make(map[string][]*TaggedCounter)
	}
	name := c.Name()
	b.tags[name] = append(b.tags[name], c)
	b.mu.Unlock()
	return c
}

func (b *Batch) TaggedCounter(name string, tags ...string) *TaggedCounter {
	c := newTaggedCounter(b, name, tags...)
	if cc, ok := b.getTag(c); ok {
		return cc
	}
	return b.addTag(c)
}

func (b *Batch) updateTag(c *TaggedCounter) error {
	// WARN: c's mutex must be locked for either reading or writing.

	if c.batch != b {
		return errors.New("batch: TaggedCounter does not belong to this Batch")
	}

	b.mu.RLock()
	tags := b.tags[c.name]
	for _, cc := range tags {
		// Skip self
		if c == cc {
			continue
		}
		cc.mu.RLock()

		// We only need to update the TaggedCounter if its
		// tags match those of an existing one.
		if matchTags(c.tags, cc.tags) {
			v := (*value)(atomic.SwapPointer(
				(*unsafe.Pointer)(unsafe.Pointer(&c.v)),
				unsafe.Pointer(cc.load()),
			))
			if v.isSet() {
				cc.Add(v.value())
			}
			cc.mu.RUnlock()
			break
		}

		cc.mu.RUnlock()
	}
	b.mu.RUnlock()

	return nil
}

func (b *Batch) Send() error {
	return nil
}

func (b *Batch) addTagged(v *TaggedCounter) {
	b.mu.RLock()
	if list, ok := b.tags[v.name]; ok {
		for _, vv := range list {
			vv.mu.RLock()
			if matchTags(vv.tags, v.tags) {
				// n := atomic.LoadUint64(v.val)
				// atomic.SwapUintptr(addr, new)
			}
			vv.mu.RUnlock()
		}
	}
	b.mu.RUnlock()
}

func matchTags(m1, m2 map[string]string) bool {
	if len(m1) != len(m2) {
		return false
	}
	for k1, v1 := range m1 {
		if v2, ok := m2[k1]; !ok || v2 != v1 {
			return false
		}
	}
	return true
}

type value struct {
	val int64
	set int32
}

func newValue(val int64) *value {
	var set int32
	if val != 0 {
		set = 1
	}
	return &value{
		val: val,
		set: set,
	}
}

func (v *value) add(val int64) int64 {
	n := atomic.AddInt64(&v.val, val)
	atomic.StoreInt32(&v.set, 1)
	return n
}

func (v *value) store(val int64) {
	atomic.StoreInt64(&v.val, val)
	atomic.StoreInt32(&v.set, 1)
}

func (v *value) flush() (n int64) {
	if atomic.CompareAndSwapInt32(&v.set, 1, 0) {
		n = atomic.SwapInt64(&v.val, 0)
	}
	return
}

func (v *value) reset() int64 {
	atomic.StoreInt32(&v.set, 0)
	return atomic.SwapInt64(&v.val, 0)
}

func (v *value) merge(vv *value) {
	if atomic.CompareAndSwapInt32(&vv.set, 1, 0) {
		v.add(vv.value())
	}
}

func (v *value) value() int64 { return atomic.LoadInt64(&v.val) }
func (v *value) isSet() bool  { return atomic.LoadInt32(&v.set) == 1 }

type Counter struct {
	name string
	v    *value
}

func newCounter(name string, val int64) *Counter {
	return &Counter{name: name, v: newValue(val)}
}

func (c *Counter) load() *value {
	return (*value)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&c.v))))
}

func (c *Counter) Name() string          { return c.name }
func (c *Counter) Value() int64          { return c.load().value() }
func (c *Counter) Add(delta int64) int64 { return c.load().add(delta) }
func (c *Counter) Increment() int64      { return c.load().add(1) }
func (c *Counter) isSet() bool           { return c.load().isSet() }

type TaggedCounter struct {
	name  string
	v     *value
	tags  map[string]string
	mu    sync.RWMutex
	batch *Batch
}

func newTaggedCounter(b *Batch, name string, tags ...string) *TaggedCounter {
	c := &TaggedCounter{
		name:  name,
		v:     newValue(0),
		batch: b,
	}
	c.setTags(tags)
	return c
}

func (c *TaggedCounter) load() *value {
	return (*value)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&c.v))))
}

func (c *TaggedCounter) Name() string          { return c.name }
func (c *TaggedCounter) Value() int64          { return c.load().value() }
func (c *TaggedCounter) Add(delta int64) int64 { return c.load().add(delta) }
func (c *TaggedCounter) Increment() int64      { return c.load().add(1) }
func (c *TaggedCounter) isSet() bool           { return c.load().isSet() }

// Write lock must be held before calling, unless called during initialization.
func (c *TaggedCounter) setTag(key, value string) bool {
	if c.tags == nil {
		c.tags = make(map[string]string)
	}
	if v, ok := c.tags[key]; !ok || v != value {
		c.tags[key] = value
		return true
	}
	return false
}

func (c *TaggedCounter) SetTag(key, value string) *TaggedCounter {
	c.mu.Lock()
	if c.setTag(key, value) {
		c.batch.updateTag(c)
	}
	c.mu.Unlock()
	return c
}

// Write lock must be held before calling, unless called during initialization.
func (c *TaggedCounter) setTags(tags []string) bool {
	if len(tags) == 0 {
		return false
	}
	if len(tags)%2 != 0 {
		panic("metric: invalid number of tags")
	}
	if c.tags == nil {
		c.tags = make(map[string]string, len(tags)/2)
	}
	update := false
	for i := 0; i < len(tags); i += 2 {
		k := tags[i+0]
		v := tags[i+1]
		update = c.setTag(k, v) || update
	}
	return update
}

func (c *TaggedCounter) SetTags(tags ...string) *TaggedCounter {
	if len(tags) == 0 {
		return c
	}
	if len(tags)%2 != 0 {
		panic("metric: invalid number of tags")
	}
	c.mu.Lock()
	if c.setTags(tags) {
		c.batch.updateTag(c)
	}
	c.mu.Unlock()
	return c
}

// WARN: Solution to potential pointer swapping race.
//
// func (c *Counter) ptr() *uint64 {
// 	return (*uint64)(atomic.LoadPointer(
// 		(*unsafe.Pointer)(unsafe.Pointer(&c.val))))
// }
