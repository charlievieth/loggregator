package metric

import (
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"testing"
)

func TestCounter(t *testing.T) {
	c := newCounter("a", 1)
	if c.Name() != "a" {
		t.Fatal("invalid name:", c.Name())
	}
	if c.Value() != 1 {
		t.Fatal("invalid initial value:", c.Value())
	}
	c.Increment()
	if c.Value() != 2 {
		t.Fatal("invalid value:", c.Value())
	}
	c.Add(2)
	if c.Value() != 4 {
		t.Fatal("invalid value:", c.Value())
	}
}

func TestBatchCounter(t *testing.T) {
	// No initialization should be required
	var b Batch

	// Original counter
	c1 := b.Counter("a")
	c1.Add(1) // 1
	if c1.Value() != 1 {
		t.Fatal("invalid value:", c1.Value())
	}

	// Duplicate counter
	c2 := b.Counter("a")
	c2.Add(1) // 2
	if c2.Value() != 2 {
		t.Fatal("invalid value:", c2.Value())
	}
	if c1.Value() != 2 {
		t.Fatal("invalid value:", c1.Value())
	}
	if c1 != c2 {
		t.Fatalf("invalid pointer: c1 (%p) c2 (%p)", c1, c2)
	}

	// Flush Batch
	if n := b.flush(); n != 2 {
		t.Fatal("bad flush:", n)
	}
	if c1.Value() != 0 {
		t.Fatal("invalid value:", c1.Value())
	}
}

func TestBatchCounterFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("short test")
	}

	procs := runtime.GOMAXPROCS(runtime.NumCPU())
	defer runtime.GOMAXPROCS(procs)

	const (
		NumFlushers = 4       // number of flush goroutines
		NumCounters = 4       // unique counters
		GoRoutines  = 20      // goroutines per counter
		Iterations  = 1000000 // add ops per counter goroutine
		ExpResult   = NumCounters * GoRoutines * Iterations
	)

	var b Batch
	wg := new(sync.WaitGroup)
	start := make(chan struct{})
	done := make(chan struct{})

	for i := 0; i < NumCounters; i++ {
		c := b.Counter(strconv.Itoa(i))
		for j := 0; j < GoRoutines; j++ {
			wg.Add(1)
			go func(c *Counter) {
				defer wg.Done()
				<-start
				for k := 0; k < Iterations; k++ {
					c.Add(1)
				}
			}(c)
		}
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	var count int64
	fwg := new(sync.WaitGroup)

	for i := 0; i < NumFlushers; i++ {
		fwg.Add(1)
		go func() {
			defer fwg.Done()
			<-start
			t := time.NewTicker(time.Millisecond)
			for {
				select {
				case <-t.C:
					atomic.AddInt64(&count, b.flush())
				case <-done:
					atomic.AddInt64(&count, b.flush())
					return
				}
			}
		}()
	}

	close(start)
	fwg.Wait()

	if count != ExpResult {
		t.Errorf("invalid count: %d expected: %d", count, ExpResult)

		// Check if any counters were not flushed.
		for _, c := range b.counters() {
			if n := c.Value(); n != 0 {
				t.Errorf("non-zero value (%d) for counter: %s", n, c.Name())
			}
		}
	}
}

func BenchmarkCounterIncrement(b *testing.B) {
	c := newCounter("a", 1)
	for i := 0; i < b.N; i++ {
		c.Increment()
	}
}

func BenchmarkCounterAdd(b *testing.B) {
	c := newCounter("a", 1)
	for i := 0; i < b.N; i++ {
		c.Add(int64(i))
	}
}

func BenchmarkCounterIncrement_Parallel(b *testing.B) {
	c := newCounter("a", 1)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Increment()
		}
	})
}

func BenchmarkCounterAdd_Parallel(b *testing.B) {
	c := newCounter("a", 1)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Add(2)
		}
	})
}

func TestInvalidTaggedCounter(t *testing.T) {
	{
		defer func() {
			if e := recover(); e == nil {
				t.Fatal("TaggedCounter should panic when passed invalid key-value pairs")
			}
		}()
		var b Batch
		b.TaggedCounter("a", "missing value")
	}
	{
		defer func() {
			if e := recover(); e == nil {
				t.Fatal("TaggedCounter should panic when passed invalid key-value pairs")
			}
		}()
		var b Batch
		c := b.TaggedCounter("a", "key", "value")
		c.SetTags("k1", "v1", "missing value")
	}
}

func TestTaggedCounter(t *testing.T) {
	var b Batch
	var exp int64

	c1 := b.TaggedCounter("a", "k1", "v1", "k2", "v2")

	if c1.Name() != "a" {
		t.Fatal("invalid name:", c1.Name())
	}
	if c1.Value() != 0 {
		t.Fatal("invalid initial value:", c1.Value())
	}

	c1.Increment()
	exp += 1
	if c1.Value() != exp {
		t.Fatal("invalid value:", c1.Value())
	}

	c1.Add(2)
	exp += 2
	if c1.Value() != exp {
		t.Fatal("invalid value:", c1.Value())
	}

	// Duplicate name and tags
	c2 := b.TaggedCounter("a", "k1", "v1", "k2", "v2")
	if c2.Name() != "a" {
		t.Fatal("invalid name:", c2.Name())
	}
	if c2.Value() != exp {
		t.Fatal("invalid initial value:", c2.Value())
	}

	c2.Add(2)
	exp += 2
	if c2.Value() != exp {
		t.Fatal("invalid value:", c2.Value())
	}
	if c1.Value() != exp {
		t.Fatal("invalid value:", c1.Value())
	}

	// Duplicate name, unique tags
	c3 := b.TaggedCounter("a", "k1", "v1", "k2", "v2", "k3", "v3")
	if c3.Name() != "a" {
		t.Fatal("invalid name:", c3.Name())
	}
	if v := c3.load(); v == c1.load() || v == c2.load() {
		t.Fatal("invalid value pointer: %p", v)
	}
	if c3.Value() == exp {
		t.Fatal("invalid initial value:", c3.Value())
	}
	c3.Add(1)

	exp += c3.Value()
	c2.SetTag("k3", "v3")
	if c2.Value() != exp {
		t.Errorf("invalid value: %d expected: %d", c2.Value(), exp)
	}
}

func TestBatchTaggedCounterInitialization(t *testing.T) {
	if testing.Short() {
		t.Skip("short test")
	}

	const (
		Count    = 100
		Delta    = 1
		Loops    = 100
		ExpValue = Count * Delta * Loops
	)

	var b Batch
	wg := new(sync.WaitGroup)
	start := make(chan struct{})

	for i := 0; i < Count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			c1 := b.TaggedCounter("a", "k1", "v1")
			c2 := b.TaggedCounter("a", "k1", "v1", "k2", "v2")
			for i := 0; i < Loops; i++ {
				c1.Add(Delta)
				c2.Add(Delta)
			}
		}()
	}

	close(start)
	wg.Wait()

	c1 := b.TaggedCounter("a", "k1", "v1")
	c2 := b.TaggedCounter("a", "k1", "v1", "k2", "v2")
	if c1.load() == c2.load() {
		t.Error("bad pointer")
	}
	if c1.Value() != ExpValue {
		t.Errorf("invalid count: %d expected: %d", c1.Value(), ExpValue)
	}
	if c2.Value() != ExpValue {
		t.Errorf("invalid count: %d expected: %d", c2.Value(), ExpValue)
	}
}
