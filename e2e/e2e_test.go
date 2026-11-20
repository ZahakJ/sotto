//go:build e2e

// Package e2e runs concord-cli against real concord peers on this machine.
//
//	CONCORD_BIN=/path/to/concord CONCORD_CLI_BIN=/path/to/concord-cli \
//	  go test -tags e2e -v ./e2e
//
// Every process gets a throwaway HOME, XDG_CONFIG_HOME, XDG_DATA_HOME and
// CONCORD_HOME under the test's temporary directory, and peers find each
// other over mDNS on this host only. No real profile is touched.
package e2e

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ZahakJ/concord-cli/internal/bot"
	"github.com/ZahakJ/concord-cli/internal/proto"
	"github.com/ZahakJ/concord-cli/internal/trust"
	"github.com/ZahakJ/concord-cli/internal/ui"
)

type person struct {
	name, root, home, data string
	env                    []string
}

func newPerson(t *testing.T, root, name string) person {
	p := person{name: name, root: filepath.Join(root, name)}
	p.home = filepath.Join(p.root, "home")
	p.data = filepath.Join(p.root, "data")
	for _, d := range []string{p.home, p.data, filepath.Join(p.root, "cfg")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	p.env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + p.home,
		"XDG_CONFIG_HOME=" + filepath.Join(p.root, "cfg"), "XDG_DATA_HOME=" + p.data,
	}
	return p
}

func (p person) identity() string { return filepath.Join(p.data, "concord-cli", "identity") }

func need(t *testing.T, key string) string {
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("%s not set", key)
	}
	return v
}

// pump feeds a client's events into a model until ok holds.
func pump(t *testing.T, who string, c *bot.Client, m *ui.Model, timeout time.Duration, ok func(string) bool) {
	t.Helper()
	deadline := time.After(timeout)
	for !ok(m.View().Content) {
		select {
		case e, open := <-c.Events():
			if !open {
				t.Fatalf("%s: concord went away: %v", who, c.Err())
			}
			m.Update(ui.EventMsg{Event: e})
		case <-deadline:
			t.Fatalf("%s: timed out; screen:\n%s", who, m.View().Content)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func typeLine(m *ui.Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
}

var sgr = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

func TestTwoPeersOverLAN(t *testing.T) {
	concord := need(t, "CONCORD_BIN")
	cli := need(t, "CONCORD_CLI_BIN")
	root := t.TempDir()
	alice, bob := newPerson(t, root, "alice"), newPerson(t, root, "bob")
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", "--", root).Run() })

	// 1. Alice creates a guild and an invite with concord's own REPL; bot
	// protocol v1 has no command for either. Harness only.
	serve := exec.Command(concord, "--serve")
	serve.Env = append(alice.env, "CONCORD_HOME="+alice.identity(), "CONCORD_PASSPHRASE=alice-pass")
	stdin, _ := serve.StdinPipe()
	stdout, _ := serve.StdoutPipe()
	serve.Stderr = io.Discard
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 256)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	expect := func(re *regexp.Regexp) []string {
		t.Helper()
		deadline := time.After(60 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatal("concord --serve exited")
				}
				if m := re.FindStringSubmatch(l); m != nil {
					return m
				}
			case <-deadline:
				t.Fatalf("concord --serve never printed %v", re)
			}
		}
	}
	io.WriteString(stdin, "create E2E\n")
	gid := expect(regexp.MustCompile(`guildID:\s+(\S+)`))[1]
	io.WriteString(stdin, "invite "+gid+"\n")
	expect(regexp.MustCompile(`invite code`))
	code := strings.TrimSpace(expect(regexp.MustCompile(`^\s*(\S{20,})\s*$`))[1])
	t.Logf("alice created guild %s and an invite (%d chars)", gid, len(code))

	// 2. Bob, through concord-cli in local mode, joins by invite while
	// Alice is online to admit him.
	bobStore, _ := trust.Load(filepath.Join(bob.data, "concord-cli", "verified.json"))
	bc, err := bot.Start(context.Background(), bot.Options{
		Binary: concord, Home: bob.identity(), Passphrase: []byte("bob-pass"), Env: bob.env, ReadyTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Close()
	bm := ui.New(bc, bobStore, bc.Ready, ui.Options{})
	bm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	start := time.Now()
	for attempt := 0; ; attempt++ {
		typeLine(bm, "/join "+code)
		joined := false
		deadline := time.After(45 * time.Second)
	wait:
		for {
			select {
			case e := <-bc.Events():
				bm.Update(ui.EventMsg{Event: e})
				if e.Type == "joined" {
					joined = true
					break wait
				}
				if e.Type == "error" {
					t.Logf("join attempt %d: %s", attempt, e.Error)
					time.Sleep(3 * time.Second)
					break wait
				}
			case <-deadline:
				break wait
			}
		}
		if joined {
			break
		}
		if attempt == 4 {
			t.Fatalf("bob could not join; screen:\n%s", bm.View().Content)
		}
	}
	t.Logf("bob joined after %s", time.Since(start).Round(time.Second))
	time.Sleep(5 * time.Second) // let the new member settle, as BOTS.md advises

	// 3. Alice leaves the REPL and comes back attached through a transport:
	// `env -i` stands in for `jaunt run <host> --` and strips the
	// environment, so her passphrase can only travel down stdin.
	io.WriteString(stdin, "quit\n")
	stdin.Close()
	_ = serve.Wait()
	via := append([]string{"env", "-i"}, alice.env...)
	ac, err := bot.Start(context.Background(), bot.Options{
		Via: via, Remote: []string{cli, "host", "--concord", concord},
		Passphrase: []byte("alice-pass"), Env: alice.env, ReadyTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	aliceStore, _ := trust.Load(filepath.Join(alice.root, "device-verified.json"))
	am := ui.New(ac, aliceStore, ac.Ready, ui.Options{Remote: true})
	am.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	time.Sleep(5 * time.Second)

	typeLine(am, "/name alice")
	typeLine(bm, "/name bob")
	pump(t, "alice", ac, am, 10*time.Second, func(s string) bool { return strings.Contains(s, "you are now alice") })
	pump(t, "bob", bc, bm, 10*time.Second, func(s string) bool { return strings.Contains(s, "you are now bob") })

	// 4. Bob writes; Alice, attached, reads it.
	typeLine(bm, "hello from bob")
	pump(t, "bob", bc, bm, 60*time.Second, func(s string) bool { return !strings.Contains(s, "sending") })
	pump(t, "alice", ac, am, 120*time.Second, func(s string) bool { return strings.Contains(s, "hello from bob") })
	t.Log("alice (attached) received bob's message")

	// 5. A hostile message from Alice's side, written raw to the protocol
	// as a modified client could, reaches Bob as inert text.
	_, gen := firstChannel(am)
	if _, err := ac.Send(proto.Command{Cmd: "send", Channel: gen,
		Text: "evil\x1b[2J\x1b]0;owned\x07\x1b]52;c;cm0gLXJmIH4=\x07\x9b6n\rdone"}); err != nil {
		t.Fatal(err)
	}
	typeLine(am, "and hello back from alice")
	pump(t, "bob", bc, bm, 120*time.Second, func(s string) bool {
		return strings.Contains(s, "hello back from alice") && strings.Contains(s, "evil")
	})
	screen := sgr.ReplaceAllString(bm.View().Content, "")
	for _, r := range screen {
		if r != '\n' && (r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)) {
			t.Fatalf("control %U reached bob's screen:\n%q", r, screen)
		}
	}
	t.Log("hostile escapes arrived as inert caret text")

	// 6. Bob verifies Alice by her full fingerprint.
	typeLine(bm, "/verify "+ac.Ready.Fingerprint)
	pump(t, "bob", bc, bm, 10*time.Second, func(s string) bool { return strings.Contains(s, "✓") })

	t.Logf("bob's screen at 80x24:\n%s", sgr.ReplaceAllString(bm.View().Content, ""))
	t.Logf("alice's screen at 100x30:\n%s", sgr.ReplaceAllString(am.View().Content, ""))

	// 7. Alice detaches (her concord keeps running on the host), Bob writes
	// while she is away, and she attaches again as if from another device.
	ac.Close()
	typeLine(bm, "written while alice was away")
	pump(t, "bob", bc, bm, 60*time.Second, func(s string) bool { return !strings.Contains(s, "sending") })
	time.Sleep(3 * time.Second)
	ac, err = bot.Start(context.Background(), bot.Options{
		Via: via, Remote: []string{cli, "host", "--concord", concord},
		Passphrase: []byte("alice-pass"), Env: alice.env, ReadyTimeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	am = ui.New(ac, aliceStore, ac.Ready, ui.Options{Remote: true})
	am.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pump(t, "alice", ac, am, 30*time.Second, func(s string) bool { return strings.Contains(s, "written while alice was away") })
	t.Log("alice re-attached and got the message sent while she was detached")

	// 8. Alice stops her host; nothing is left running.
	typeLine(am, "/shutdown")
	ac.Close()
	bc.Close()
	sock := filepath.Join(alice.data, "concord-cli", "host.sock")
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if _, err := os.Stat(sock); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("alice's host still listening after /shutdown")
		}
	}
	if out, _ := exec.Command("pgrep", "-f", root).Output(); len(strings.TrimSpace(string(out))) > 0 {
		t.Fatalf("processes left running: %s", out)
	}
}

// firstChannel returns the guild and channel ids the model shows first.
func firstChannel(m *ui.Model) (string, string) { return m.Active() }
