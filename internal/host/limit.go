package host

import (
	"sync"
	"sync/atomic"
	"time"
)

// limiter spaces passphrase checks across every connection to the daemon:
// one check at a time, at least base apart, and after n failures in a row
// base·2^(n-1) apart, up to max. A success resets the backoff.
type limiter struct {
	mu        sync.Mutex
	next      time.Time
	fails     int
	base, max time.Duration
	now       func() time.Time
	sleep     func(time.Duration)
	nextNano  atomic.Int64 // next, readable without waiting for mu
}

// pending is how long a check started now would wait, as far as is known.
func (l *limiter) pending() time.Duration {
	return max(0, time.Unix(0, l.nextNano.Load()).Sub(l.now()))
}

func newLimiter(base, max time.Duration) *limiter {
	return &limiter{base: base, max: max, now: time.Now, sleep: time.Sleep}
}

// attempt waits for the next slot, runs check in it, and records the result.
func (l *limiter) attempt(check func() bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if d := l.next.Sub(l.now()); d > 0 {
		l.sleep(d)
	}
	ok := check()
	gap := l.base
	if ok {
		l.fails = 0
	} else {
		l.fails = min(l.fails+1, 30)
		gap = min(l.base<<(l.fails-1), l.max)
	}
	l.next = l.now().Add(gap)
	l.nextNano.Store(l.next.UnixNano())
	return ok
}
