package host

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ZahakJ/concord-cli/internal/bot"
	"github.com/ZahakJ/concord-cli/internal/proto"
)

type fakeClock struct {
	t     time.Time
	slept []time.Duration
}

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) sleep(d time.Duration) {
	c.slept = append(c.slept, d)
	c.t = c.t.Add(d)
}

func TestLimiterBacksOffExponentiallyAndResets(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1e9, 0)}
	l := newLimiter(time.Second, time.Minute)
	l.now, l.sleep = clk.now, clk.sleep
	for range 8 {
		l.attempt(func() bool { return false })
	}
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60} // seconds, capped at a minute
	if len(clk.slept) != len(want) {
		t.Fatalf("slept %v", clk.slept)
	}
	for i, w := range want {
		if clk.slept[i] != w*time.Second {
			t.Fatalf("wait %d = %v, want %vs (all: %v)", i, clk.slept[i], w, clk.slept)
		}
	}
	clk.slept = nil
	l.attempt(func() bool { return true }) // waits out the last backoff, then resets
	l.attempt(func() bool { return false })
	if len(clk.slept) != 2 || clk.slept[1] != time.Second {
		t.Fatalf("after success: %v", clk.slept)
	}
}

// Ported from the review's TestPoCHost (a): 400 parallel wrong guesses were
// all answered within about a second.
func TestParallelGuessesAreRateLimited(t *testing.T) {
	base := t.TempDir()
	sock := filepath.Join(base, "host.sock")
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", "--", "--home "+base).Run() })
	c1, err := bot.Start(context.Background(), attach(t, base, "pw"))
	if err != nil {
		t.Fatal(err)
	}
	c1.Close()
	time.Sleep(500 * time.Millisecond)

	var answered, busy atomic.Int32
	var wg sync.WaitGroup
	for i := range 400 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.Dial("unix", sock)
			if err != nil {
				return
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(4 * time.Second))
			_ = proto.WriteHello(c, []byte(fmt.Sprintf("guess%d", i)))
			l, _ := bufio.NewReader(c).ReadString('\n')
			switch {
			case strings.Contains(l, "wrong passphrase"):
				answered.Add(1)
			case strings.Contains(l, "busy"):
				busy.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := answered.Load(); n > 4 {
		t.Fatalf("%d guesses answered in 4s; want at most 4 (one a second, backing off)", n)
	}
	if busy.Load() == 0 {
		t.Fatal("no connection was turned away; unauthenticated connections are not capped")
	}

	// The owner still gets in once the backoff has passed.
	o := attach(t, base, "pw")
	o.ReadyTimeout = 90 * time.Second
	c2, err := bot.Start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c2.Send(proto.Command{Cmd: "host-stop"})
	c2.Close()
}
