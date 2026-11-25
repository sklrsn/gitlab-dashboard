// Package cache is a short-lived in-memory TTL cache that also merges
// concurrent identical requests into one upstream call.
package cache

import (
	"context"
	"sync"
	"time"
)

type entry struct {
	val     any
	expires time.Time
}

type call struct {
	done chan struct{}
	val  any
	err  error
}

type Cache struct {
	ttl      time.Duration
	now      func() time.Time
	mu       sync.Mutex
	items    map[string]entry
	inflight map[string]*call
}

func New(ttl time.Duration) *Cache {
	return &Cache{ttl: ttl, now: time.Now, items: map[string]entry{}, inflight: map[string]*call{}}
}

// Do returns the cached value for key, or runs fn once for all concurrent
// callers. Errors are not cached.
func Do[T any](ctx context.Context, c *Cache, key string, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	c.mu.Lock()
	if e, ok := c.items[key]; ok && c.now().Before(e.expires) {
		c.mu.Unlock()
		return e.val.(T), nil
	}
	if cl, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-cl.done:
			if cl.err != nil {
				return zero, cl.err
			}
			return cl.val.(T), nil
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
	cl := &call{done: make(chan struct{})}
	c.inflight[key] = cl
	c.mu.Unlock()

	// Detached from the first caller's cancellation so other waiters still get a result.
	v, err := fn(context.WithoutCancel(ctx))

	c.mu.Lock()
	delete(c.inflight, key)
	if err == nil && c.ttl > 0 {
		c.items[key] = entry{val: v, expires: c.now().Add(c.ttl)}
	}
	c.sweepLocked()
	c.mu.Unlock()

	cl.val, cl.err = v, err
	close(cl.done)
	if err != nil {
		return zero, err
	}
	return v, nil
}

func (c *Cache) sweepLocked() {
	if len(c.items) < 512 {
		return
	}
	now := c.now()
	for k, e := range c.items {
		if !now.Before(e.expires) {
			delete(c.items, k)
		}
	}
}
