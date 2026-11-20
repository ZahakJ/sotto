// Package host lets one headless concord serve a person on several devices.
//
// `concord-cli host` runs on the machine that keeps the identity. It is not
// started by hand: a client on another device runs it through a transport
// that already exists between the two machines (a mesh's `jaunt run`, or
// ssh), so the host never opens a port. Its stdin and stdout are the
// transport's.
//
// The first host started on a machine also starts a small daemon, detached
// from the transport session, that keeps `concord --bot` running and listens
// on a Unix socket only its user can open. Later attaches, from any device,
// are relayed to that daemon. While no one is attached the daemon keeps the
// last few hundred events in memory and replays them on the next attach.
//
// The passphrase is typed on the device the person is at, crosses the
// transport as the first stdin line, and is never written to disk on the
// host. The daemon keeps only a keyed hash of it, to check later attaches;
// concord itself receives it through its environment, which is the only way
// concord accepts one.
package host

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ZahakJ/concord-cli/internal/bot"
	"github.com/ZahakJ/concord-cli/internal/paths"
	"github.com/ZahakJ/concord-cli/internal/proto"
)

// Config locates everything the host needs on its machine.
type Config struct {
	Self         string // this executable, to start the daemon
	Concord      string // the concord binary
	Home         string // CONCORD_HOME
	Sock         string // the daemon's socket
	Env          []string
	ReadyTimeout time.Duration
}

// missedCap bounds the events kept for a client that is not attached.
const missedCap = 500

// maxUnauth caps connections waiting to authenticate; more are refused.
const maxUnauth = 4

// Main is `concord-cli host [--concord PATH] [--home DIR]`.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("concord-cli host", flag.ContinueOnError)
	fs.SetOutput(stderr)
	concord := fs.String("concord", "", "concord binary on this machine (default: $CONCORD_BIN, then $PATH)")
	base := fs.String("home", "", "data directory (default: $XDG_DATA_HOME/concord-cli)")
	daemon := fs.Bool("daemon", false, "internal: run the daemon")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	env := os.Environ()
	cfg := Config{Env: env, ReadyTimeout: 2 * time.Minute}
	if *base == "" {
		b, err := paths.Base(env)
		if err != nil {
			fmt.Fprintln(stderr, "concord-cli host:", err)
			return 1
		}
		*base = b
	}
	cfg.Home, cfg.Sock = paths.Identity(*base), paths.Sock(*base)
	if err := paths.Guard(cfg.Home, env); err != nil {
		fmt.Fprintln(stderr, "concord-cli host:", err)
		return 1
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "concord-cli host:", err)
		return 1
	}
	cfg.Self = self
	cfg.Concord = *concord
	if *daemon {
		return runDaemon(cfg)
	}
	if err := Attach(cfg, stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "concord-cli host:", err)
		return 1
	}
	return 0
}

// Attach serves one client on stdin/stdout: it reads the handshake, makes
// sure the daemon runs (starting it with this passphrase if not), and relays.
func Attach(cfg Config, stdin io.Reader, stdout io.Writer) error {
	fresh := paths.Fresh(cfg.Home)
	if c, err := net.Dial("unix", cfg.Sock); err == nil {
		c.Close()
		fresh = false // a daemon runs, whatever is on disk
	}
	if err := proto.WriteGreeting(stdout, fresh); err != nil {
		return err
	}
	in := bufio.NewReader(stdin)
	pass, err := proto.ReadHello(in)
	if err != nil {
		return err
	}
	defer clear(pass)

	conn, err := dialOrStart(cfg, pass)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := proto.WriteHello(conn, pass); err != nil {
		return err
	}
	clear(pass)
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	status, err := br.ReadString('\n')
	if err != nil {
		return errors.New("the host daemon closed the connection")
	}
	_ = conn.SetReadDeadline(time.Time{})
	if status = strings.TrimSpace(status); status != "OK" {
		return errors.New(strings.TrimPrefix(status, "ERR "))
	}

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(conn, in)
		_ = conn.(*net.UnixConn).CloseWrite()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(stdout, br)
		done <- struct{}{}
	}()
	<-done
	return nil
}

// dialOrStart connects to the daemon, starting it first if none answers.
// A lock file serialises two clients attaching at once.
func dialOrStart(cfg Config, pass []byte) (net.Conn, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.Sock), 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(cfg.Sock+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	if c, err := net.Dial("unix", cfg.Sock); err == nil {
		return c, nil
	}
	if err := startDaemon(cfg, pass); err != nil {
		return nil, err
	}
	return net.Dial("unix", cfg.Sock)
}

// startDaemon re-executes this binary as the daemon, in its own session so
// that it outlives the transport. The passphrase goes over a pipe (fd 3);
// the daemon reports "READY" or "ERR …" on another (fd 4).
func startDaemon(cfg Config, pass []byte) error {
	passR, passW, err := os.Pipe()
	if err != nil {
		return err
	}
	statR, statW, err := os.Pipe()
	if err != nil {
		passR.Close()
		passW.Close()
		return err
	}
	args := []string{"host", "--daemon", "--home", filepath.Dir(cfg.Home)}
	if cfg.Concord != "" {
		args = append(args, "--concord", cfg.Concord)
	}
	cmd := exec.Command(cfg.Self, args...)
	cmd.Env = cfg.Env
	cmd.ExtraFiles = []*os.File{passR, statW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	passR.Close()
	statW.Close()
	if err != nil {
		passW.Close()
		statR.Close()
		return err
	}
	_, werr := passW.Write(pass)
	passW.Close()
	status, _ := bufio.NewReader(statR).ReadString('\n')
	statR.Close()
	if werr != nil {
		return werr
	}
	status = strings.TrimSpace(status)
	if status != "READY" {
		_ = cmd.Wait()
		if status == "" {
			return errors.New("the host daemon failed to start")
		}
		return errors.New(strings.TrimPrefix(status, "ERR "))
	}
	go func() { _ = cmd.Wait() }() // reap if it exits while we still run
	return nil
}

// ---- daemon ----

type daemon struct {
	c    *bot.Client
	key  []byte
	mac  []byte
	ln   net.Listener
	sock string

	limit  *limiter      // one passphrase check at a time, daemon-wide
	unauth chan struct{} // slots for connections not yet authenticated

	mu      sync.Mutex
	cur     net.Conn
	ids     map[uint64]json.RawMessage // concord's id -> the attached client's
	missed  [][]byte                   // kept until the attached client acknowledges a replay
	dropped int                        // events pushed out of missed since the last replay
	acked   bool                       // the attached client has acknowledged its replay
}

// store keeps an event for replay, dropping the oldest past missedCap.
func (d *daemon) store(line []byte) {
	d.missed = append(d.missed, line)
	if len(d.missed) > missedCap {
		d.missed = d.missed[1:]
		d.dropped++
	}
}

func runDaemon(cfg Config) int {
	passF, statF := os.NewFile(3, "pass"), os.NewFile(4, "status")
	report := func(s string) {
		fmt.Fprintln(statF, s)
		statF.Close()
	}
	pass, err := io.ReadAll(io.LimitReader(passF, 4096))
	passF.Close()
	if err != nil || len(pass) == 0 {
		report("ERR no passphrase")
		return 1
	}
	d := &daemon{
		key: make([]byte, 32), sock: cfg.Sock,
		limit:  newLimiter(time.Second, 5*time.Minute),
		unauth: make(chan struct{}, maxUnauth),
	}
	if _, err := rand.Read(d.key); err != nil {
		clear(pass)
		report("ERR " + err.Error())
		return 1
	}
	d.mac = d.sum(pass)

	binPath, err := bot.Locate(cfg.Concord, cfg.Env)
	if err == nil {
		_, err = bot.Check(context.Background(), binPath, cfg.Env)
	}
	if err != nil {
		clear(pass)
		report("ERR " + err.Error())
		return 1
	}
	c, err := bot.Start(context.Background(), bot.Options{
		Binary: binPath, Home: cfg.Home, Passphrase: pass, Env: cfg.Env, ReadyTimeout: cfg.ReadyTimeout,
	})
	if err != nil {
		report("ERR " + err.Error())
		return 1
	}
	d.c = c
	_ = os.Remove(cfg.Sock)
	old := syscall.Umask(0o077)
	ln, err := net.Listen("unix", cfg.Sock)
	syscall.Umask(old)
	if err != nil {
		c.Close()
		report("ERR " + err.Error())
		return 1
	}
	d.ln = ln
	report("READY")
	// Detached: nothing of ours writes to the transport's terminal.
	devnull, _ := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	os.Stdin, os.Stdout, os.Stderr = devnull, devnull, devnull

	go d.accept()
	d.pump()
	ln.Close()
	_ = os.Remove(cfg.Sock)
	return 0
}

func (d *daemon) sum(pass []byte) []byte {
	h := hmac.New(sha256.New, d.key)
	h.Write(pass)
	return h.Sum(nil)
}

// pump relays concord's events to whoever is attached, or keeps them.
func (d *daemon) pump() {
	for e := range d.c.Events() {
		d.mu.Lock()
		if id := e.ReqID(); id != 0 {
			cid, ok := d.ids[id]
			delete(d.ids, id)
			if !ok {
				d.mu.Unlock()
				continue // a reply for a client that has gone
			}
			e.ID = cid
		}
		line, err := proto.EncodeEvent(e)
		if err == nil {
			if d.cur != nil {
				d.writeLocked(line)
			}
			// Until the client acknowledges its replay, what it is sent is
			// kept too, so a connection that dies early loses nothing.
			if keep(e.Type) && (d.cur == nil || !d.acked) {
				d.store(line)
			}
		}
		d.mu.Unlock()
	}
}

func keep(typ string) bool {
	switch typ {
	case "message", "edit", "delete", "member", "guild":
		return true
	}
	return false
}

func (d *daemon) writeLocked(line []byte) {
	_ = d.cur.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := d.cur.Write(line); err != nil {
		d.cur.Close()
		d.cur = nil
	}
}

func (d *daemon) accept() {
	for {
		conn, err := d.ln.Accept()
		if err != nil {
			return
		}
		go d.serve(conn)
	}
}

func (d *daemon) serve(conn net.Conn) {
	select {
	case d.unauth <- struct{}{}:
	default:
		fmt.Fprintln(conn, "ERR busy: too many connections waiting to authenticate")
		conn.Close()
		return
	}
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	pass, err := proto.ReadHello(br)
	if err != nil {
		<-d.unauth
		fmt.Fprintf(conn, "ERR %v\n", err)
		conn.Close()
		return
	}
	ok := d.limit.attempt(func() bool { return hmac.Equal(d.sum(pass), d.mac) })
	clear(pass)
	<-d.unauth
	if !ok {
		fmt.Fprintln(conn, "ERR wrong passphrase for the running host")
		conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	d.mu.Lock()
	if d.cur != nil {
		d.cur.Close() // the newest attach wins; the old device is detached
	}
	d.cur, d.ids = conn, map[uint64]json.RawMessage{}
	fmt.Fprintln(conn, "OK")
	if line, err := proto.EncodeEvent(d.c.Ready); err == nil {
		d.writeLocked(line)
	}
	for _, line := range d.missed {
		if d.cur == nil {
			break
		}
		d.writeLocked(line)
	}
	// The buffer is cleared only when the client answers this marker with
	// host-ack; an attach that dies first leaves it for the next one.
	d.acked = false
	if d.cur != nil {
		b, _ := json.Marshal(map[string]any{"type": "replayed", "count": len(d.missed), "dropped": d.dropped})
		d.writeLocked(append(b, '\n'))
	}
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		if d.cur == conn {
			d.cur = nil
		}
		d.mu.Unlock()
		conn.Close()
	}()
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 1<<20 {
			return
		}
		if len(strings.TrimSpace(string(line))) > 0 {
			if !d.command(conn, line) {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// command forwards one client line to concord, mapping its id. It reports
// false when the client is done.
func (d *daemon) command(conn net.Conn, line []byte) bool {
	var raw struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(line, &raw)
	var c proto.Command
	reply := func(v map[string]any) {
		if len(raw.ID) > 0 {
			v["id"] = raw.ID
		}
		b, _ := json.Marshal(v)
		d.mu.Lock()
		if d.cur == conn {
			d.writeLocked(append(b, '\n'))
		}
		d.mu.Unlock()
	}
	if err := json.Unmarshal(line, &c); err != nil {
		reply(map[string]any{"type": "error", "error": "bad command line"})
		return true
	}
	switch c.Cmd {
	case "quit": // detach; concord keeps running
		reply(map[string]any{"type": "ok", "cmd": "quit"})
		return false
	case "host-ack": // the client has the replay
		d.mu.Lock()
		if d.cur == conn {
			d.missed, d.dropped, d.acked = nil, 0, true
		}
		d.mu.Unlock()
		return true
	case "host-stop":
		reply(map[string]any{"type": "ok", "cmd": "host-stop"})
		go d.c.Close()
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cur != conn {
		return false
	}
	id, err := d.c.Send(c)
	if err != nil {
		b, _ := json.Marshal(map[string]any{"type": "error", "error": err.Error(), "id": raw.ID})
		d.writeLocked(append(b, '\n'))
		return true
	}
	d.ids[id] = raw.ID
	return true
}
