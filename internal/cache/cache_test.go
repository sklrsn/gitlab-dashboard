package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentCallsAreMerged(t *testing.T) {
	c := New(time.Minute)
	var calls atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := Do(context.Background(), c, "k", func(context.Context) (int, error) {
				calls.Add(1)
				<-release
				return 42, nil
			})
			if err != nil || v != 42 {
				t.Errorf("got %v, %v", v, err)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("fn ran %d times, want 1", calls.Load())
	}
}

func TestExpiryAndErrorsNotCached(t *testing.T) {
	c := New(time.Second)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	n := 0
	fn := func(context.Context) (int, error) { n++; return n, nil }

	if v, _ := Do(context.Background(), c, "k", fn); v != 1 {
		t.Fatalf("v=%d", v)
	}
	if v, _ := Do(context.Background(), c, "k", fn); v != 1 {
		t.Fatalf("cached v=%d", v)
	}
	now = now.Add(2 * time.Second)
	if v, _ := Do(context.Background(), c, "k", fn); v != 2 {
		t.Fatalf("expired v=%d", v)
	}

	fails := 0
	bad := func(context.Context) (int, error) { fails++; return 0, errors.New("boom") }
	Do(context.Background(), c, "e", bad)
	Do(context.Background(), c, "e", bad)
	if fails != 2 {
		t.Fatalf("errors must not be cached, fn ran %d times", fails)
	}
}
