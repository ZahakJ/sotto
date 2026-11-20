// Package bot runs `concord --bot` as a child process and speaks its line
// protocol (docs/BOTS.md in the Concord repository).
//
// The passphrase reaches the child only through its environment, at start;
// nothing here writes it, or any message, to a log or a file.
package bot

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ZahakJ/concord-cli/internal/proto"
	"github.com/ZahakJ/concord-cli/internal/safe"
)

var (
	// ErrNotFound means there is no concord binary to run.
	ErrNotFound = errors.New("concord not found")
	// ErrTooOld means the binary predates the bot protocol this client speaks.
	ErrTooOld = errors.New("concord is too old")
)

// maxEventLine bounds one stdout line. Commands are capped at 1 MiB by
// concord; an event carries at most one message, plus JSON escaping.
const maxEventLine = 8 << 20

// Options configures Start.
type Options struct {
	Binary     string   // path to the concord executable
	Home       string   // CONCORD_HOME for the child
	Passphrase []byte   // handed to the child, then zeroed
	Env        []string // base environment; CONCORD_HOME/PASSPHRASE in it are replaced
	// ReadyTimeout bounds unlocking the identity and starting the network.
	ReadyTimeout time.Duration

	// Via, when set, attaches to a host on another machine instead of
	// running concord here: Via is the transport command (say
	// ["jaunt","run","box","--"] or ["ssh","box"]) and Remote the command it
	// runs there (["concord-cli","host"]). The passphrase then goes down the
	// transport's stdin as the first line, not into any environment.
	Via    []string
	Remote []string

	// Ask, when attaching, supplies the passphrase once the host has said
	// whether it holds an identity yet; confirm is true when it does not.
	// Without Ask, Passphrase is used.
	Ask func(confirm bool) ([]byte, error)
}

// Client is a running `concord --bot`.
type Client struct {
	// Ready is the handshake event: own fingerprint, name, guilds.
	Ready proto.Event

	cmd    *exec.Cmd
	stdin  *os.File
	events chan proto.Event
	nextID atomic.Uint64
	stderr *tail
	remote bool // attached to a host daemon rather than a local concord

	writeMu sync.Mutex
	closed  bool

	done    chan struct{} // closed once the child has exited
	exitErr error
	once    sync.Once
}

// Locate finds the concord binary: an explicit path, then $CONCORD_BIN, then
// $PATH (as given in env).
func Locate(explicit string, env []string) (string, error) {
	if explicit == "" {
		explicit = lookup(env, "CONCORD_BIN")
	}
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("%w at %s", ErrNotFound, explicit)
		}
		return explicit, nil
	}
	for _, dir := range strings.Split(lookup(env, "PATH"), string(os.PathListSeparator)) {
		if dir == "" {
			continue
		}
		p := dir + string(os.PathSeparator) + "concord"
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w on $PATH (install the headless build, `make cli` in the Concord repository, or pass --concord)", ErrNotFound)
}

// Check runs `concord --version` and `concord --help` and refuses a binary
// without bot mode. It returns the reported release ("dev" for a local build).
func Check(ctx context.Context, bin string, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	run := func(arg string) (string, error) {
		cmd := exec.CommandContext(ctx, bin, arg)
		cmd.Env = withoutSecrets(env)
		out, err := cmd.Output()
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w at %s", ErrNotFound, bin)
		}
		if err != nil {
			return "", fmt.Errorf("%s %s: %v", bin, arg, err)
		}
		return string(out), nil
	}
	out, err := run("--version")
	if err != nil {
		return "", err
	}
	v, err := proto.ParseVersion(out)
	if err != nil {
		return "", err
	}
	if proto.TooOld(v) {
		return v, fmt.Errorf("%w: it reports %s; bot mode needs %s or later", ErrTooOld, v, proto.MinRelease)
	}
	help, err := run("--help")
	if err != nil {
		return v, err
	}
	if !strings.Contains(help, "--bot") {
		return v, fmt.Errorf("%w: %s (%s) has no --bot mode", ErrTooOld, bin, v)
	}
	return v, nil
}

// Start launches `concord --bot` and waits for its ready event. The
// passphrase is zeroed before Start returns, whatever the outcome.
func Start(ctx context.Context, o Options) (*Client, error) {
	defer clear(o.Passphrase)
	bin, args := o.Binary, []string{"--bot"}
	var env []string
	if len(o.Via) > 0 {
		bin, args = o.Via[0], append(slices.Clone(o.Via[1:]), o.Remote...)
		env = withoutSecrets(o.Env)
	} else {
		env = childEnv(o.Env, o.Home, o.Passphrase)
		clear(o.Passphrase)
	}
	defer func() {
		// The env entry is a Go string and cannot be wiped; drop our
		// reference so it is at least collectable.
		clear(env)
	}()

	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	c := &Client{
		stdin:  inW,
		events: make(chan proto.Event, 1024),
		stderr: newTail(20),
		remote: len(o.Via) > 0,
		done:   make(chan struct{}),
	}
	c.cmd = exec.Command(bin, args...)
	c.cmd.Env = env
	c.cmd.Stdin, c.cmd.Stdout, c.cmd.Stderr = inR, outW, c.stderr
	c.cmd.SysProcAttr = sysProcAttr()
	err = c.cmd.Start()
	c.cmd.Env = nil
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w at %s", ErrNotFound, bin)
		}
		return nil, err
	}
	go func() {
		c.exitErr = c.cmd.Wait()
		close(c.done)
	}()
	timeout := o.ReadyTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	br := bufio.NewReaderSize(outR, 64*1024)
	if len(o.Via) > 0 {
		if err := c.hello(br, o, timeout); err != nil {
			c.kill()
			outR.Close()
			return nil, fmt.Errorf("attaching via %s: %v%s", bin, err, c.stderr.hint())
		}
	}

	first := make(chan proto.Event, 1)
	firstErr := make(chan error, 1)
	go c.read(outR, br, first, firstErr)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	fail := func(err error) (*Client, error) {
		c.kill()
		return nil, err
	}
	select {
	case e := <-first:
		if err := proto.CheckReady(e); err != nil {
			return fail(err)
		}
		c.Ready = e
		return c, nil
	case err := <-firstErr:
		// Usually the child is exiting and its stderr says why; a child that
		// merely spoke nonsense is still running and gets killed.
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
		}
		return fail(c.exitReason(err))
	case <-timer.C:
		return fail(fmt.Errorf("concord did not become ready within %s%s", timeout, c.stderr.hint()))
	case <-ctx.Done():
		return fail(ctx.Err())
	}
}

// read decodes stdout. The first event goes to first (or an error to
// firstErr); the rest to c.events, which closes when stdout does.
// hello reads the host's greeting, gets the passphrase (asking twice when
// the host is new), and sends the handshake.
func (c *Client) hello(br *bufio.Reader, o Options, timeout time.Duration) error {
	type greeting struct {
		fresh bool
		err   error
	}
	got := make(chan greeting, 1)
	go func() {
		fresh, err := proto.ReadGreeting(br)
		got <- greeting{fresh, err}
	}()
	var g greeting
	select {
	case g = <-got:
	case <-time.After(timeout):
		c.kill() // unblocks the reader
		return fmt.Errorf("no greeting from the host within %s", timeout)
	}
	if g.err != nil {
		return g.err
	}
	pass := o.Passphrase
	if o.Ask != nil {
		var err error
		if pass, err = o.Ask(g.fresh); err != nil {
			return err
		}
		defer clear(pass)
	}
	return proto.WriteHello(c.stdin, pass)
}

func (c *Client) read(r io.ReadCloser, br *bufio.Reader, first chan<- proto.Event, firstErr chan<- error) {
	defer r.Close()
	defer close(c.events)
	handshake := true
	for {
		line, err := readLine(br, maxEventLine)
		if len(bytes.TrimSpace(line)) > 0 {
			e, derr := proto.Decode(line)
			if handshake {
				handshake = false
				if derr != nil {
					firstErr <- fmt.Errorf("concord's first line is not protocol v%d: %v", proto.Version, derr)
					return
				}
				first <- e
				continue
			}
			if derr != nil {
				e = proto.Event{Type: "client-error", Error: derr.Error()}
			}
			c.events <- e
			if e.Type == "replayed" && c.remote {
				// Everything before the marker is in hand; the host may
				// now forget it.
				_, _ = c.Send(proto.Command{Cmd: "host-ack"})
			}
		}
		if err == errLong {
			if !handshake {
				c.events <- proto.Event{Type: "client-error", Error: "discarded an oversized line from concord"}
			}
			continue
		}
		if err != nil {
			if handshake {
				firstErr <- io.ErrUnexpectedEOF
			}
			return
		}
	}
}

var errLong = errors.New("line too long")

func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	over := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !over {
			if len(buf)+len(chunk) > max {
				over, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if over && err == nil {
			return nil, errLong
		}
		return buf, err
	}
}

// Events delivers everything after ready. It closes when concord exits.
func (c *Client) Events() <-chan proto.Event { return c.events }

// Done closes when the child process has exited.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err explains why the child exited; valid after Done.
func (c *Client) Err() error { return c.exitReason(nil) }

// Send writes one command and returns the id its reply will carry.
func (c *Client) Send(cmd proto.Command) (uint64, error) {
	cmd.ID = c.nextID.Add(1)
	line, err := proto.Encode(cmd)
	if err != nil {
		return 0, err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return 0, errors.New("concord is not running")
	}
	// A child that stops reading must not freeze the interface.
	_ = c.stdin.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.stdin.Write(line); err != nil {
		return 0, fmt.Errorf("writing to concord: %w", err)
	}
	return cmd.ID, nil
}

// Close asks concord to quit, then insists. It returns once the child is gone.
func (c *Client) Close() error {
	c.once.Do(func() {
		_, _ = c.Send(proto.Command{Cmd: "quit"})
		c.writeMu.Lock()
		c.closed = true
		c.stdin.Close() // end of stdin also means quit
		c.writeMu.Unlock()
		select {
		case <-c.done:
			return
		case <-time.After(5 * time.Second):
		}
		_ = c.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-c.done:
			return
		case <-time.After(3 * time.Second):
		}
		_ = c.cmd.Process.Kill()
		<-c.done
	})
	return nil
}

func (c *Client) kill() {
	c.once.Do(func() {
		c.writeMu.Lock()
		c.closed = true
		c.stdin.Close()
		c.writeMu.Unlock()
		_ = c.cmd.Process.Kill()
		<-c.done
	})
}

func (c *Client) exitReason(cause error) error {
	select {
	case <-c.done:
	default:
		if cause != nil {
			return cause
		}
		return nil
	}
	if last := c.stderr.last(); last != "" {
		return fmt.Errorf("concord exited: %s", last)
	}
	if c.exitErr != nil {
		return fmt.Errorf("concord exited: %v", c.exitErr)
	}
	if cause != nil {
		return fmt.Errorf("concord exited: %v", cause)
	}
	return errors.New("concord exited")
}

// childEnv is base without any CONCORD_HOME or CONCORD_PASSPHRASE, plus ours.
func childEnv(base []string, home string, pass []byte) []string {
	env := withoutSecrets(base)
	env = slices.DeleteFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "CONCORD_HOME=") })
	return append(env, "CONCORD_HOME="+home, "CONCORD_PASSPHRASE="+string(pass))
}

func withoutSecrets(env []string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		return strings.HasPrefix(kv, "CONCORD_PASSPHRASE=")
	})
}

func lookup(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], key+"="); ok {
			return v
		}
	}
	return ""
}

// tail keeps the last few lines of the child's stderr, in memory only, to
// explain a failure to start. They are cleaned before they are shown.
type tail struct {
	mu    sync.Mutex
	lines []string
	part  []byte
	max   int
}

func newTail(max int) *tail { return &tail{max: max} }

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.part = append(t.part, p...)
	for {
		i := bytes.IndexByte(t.part, '\n')
		if i < 0 {
			break
		}
		t.push(string(t.part[:i]))
		t.part = t.part[i+1:]
	}
	if len(t.part) > 4096 {
		t.push(string(t.part))
		t.part = nil
	}
	return len(p), nil
}

func (t *tail) push(s string) {
	s = strings.TrimSpace(safe.Line(s))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	if s == "" {
		return
	}
	t.lines = append(t.lines, s)
	if len(t.lines) > t.max {
		t.lines = t.lines[1:]
	}
}

func (t *tail) last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.part) > 0 {
		t.push(string(t.part))
		t.part = nil
	}
	if len(t.lines) == 0 {
		return ""
	}
	return strings.TrimPrefix(t.lines[len(t.lines)-1], "concord: ")
}

func (t *tail) hint() string {
	if l := t.last(); l != "" {
		return " (last said: " + l + ")"
	}
	return ""
}
