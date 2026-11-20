// Command sotto is a small terminal client for Concord. It drives the
// headless `concord --bot` over its line protocol, either here or, attached
// over an existing transport, on another of your machines.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/ZahakJ/sotto/internal/bot"
	"github.com/ZahakJ/sotto/internal/host"
	"github.com/ZahakJ/sotto/internal/paths"
	"github.com/ZahakJ/sotto/internal/proto"
	"github.com/ZahakJ/sotto/internal/trust"
	"github.com/ZahakJ/sotto/internal/ui"
)

// version is stamped at release time with -ldflags "-X main.version=…".
var version = "dev"

const usage = `sotto: a terminal client for Concord

Usage:
  sotto                   run concord here, with its own identity
  sotto --via 'ssh box'   attach to the identity kept on another machine
  sotto host              the far end of --via (started by the transport)

Flags:
  --concord PATH   the concord binary (default: $CONCORD_BIN, then $PATH)
  --home DIR       data directory (default: $XDG_DATA_HOME/sotto)
  --via CMD        transport to the host, split on spaces, e.g. 'jaunt run box --'
  --remote CMD     what the transport runs (default: 'sotto host')
  --version        print versions and exit

Keys: ctrl-k switch · tab members · /help for the rest.
`

func main() {
	if len(os.Args) > 1 && os.Args[1] == "host" {
		os.Exit(host.Main(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sotto:", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("sotto", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	concord := fs.String("concord", "", "")
	baseFlag := fs.String("home", "", "")
	via := fs.String("via", "", "")
	remote := fs.String("remote", "sotto host", "")
	showVersion := fs.Bool("version", false, "")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (see --help)", fs.Arg(0))
	}
	env := os.Environ()

	base := *baseFlag
	if base == "" {
		b, err := paths.Base(env)
		if err != nil {
			return err
		}
		base = b
	}
	opts := bot.Options{Home: paths.Identity(base), Env: env, ReadyTimeout: 2 * time.Minute}
	remoteMode := *via != ""

	if *showVersion {
		fmt.Printf("sotto %s (bot protocol v%d)\n", version, proto.Version)
		if remoteMode {
			return nil
		}
		bin, err := bot.Locate(*concord, env)
		if err != nil {
			return err
		}
		v, err := bot.Check(context.Background(), bin, env)
		if err != nil {
			return err
		}
		fmt.Printf("concord %s at %s\n", v, bin)
		return nil
	}

	if remoteMode {
		opts.Via, opts.Remote = strings.Fields(*via), strings.Fields(*remote)
		if len(opts.Via) == 0 || len(opts.Remote) == 0 {
			return errors.New("--via and --remote must name commands")
		}
	} else {
		if err := paths.Guard(opts.Home, env); err != nil {
			return err
		}
		bin, err := bot.Locate(*concord, env)
		if err != nil {
			return err
		}
		if _, err := bot.Check(context.Background(), bin, env); err != nil {
			return err
		}
		opts.Binary = bin
		if err := os.MkdirAll(base, 0o700); err != nil {
			return err
		}
	}
	store, err := trust.Load(paths.Trust(base))
	if err != nil {
		return err
	}

	if remoteMode {
		// The host says first whether it holds an identity yet; a new one
		// gets its passphrase typed twice, as locally.
		opts.Ask = askPassphrase
		opts.Wait = func(secs int) {
			fmt.Fprintf(os.Stderr, "too many attempts, wait %ds …\n", secs)
		}
		fmt.Fprintf(os.Stderr, "attaching via %s …\n", opts.Via[0])
	} else {
		pass, err := askPassphrase(paths.Fresh(opts.Home))
		if err != nil {
			return err
		}
		opts.Passphrase = pass // Start zeroes it
		fmt.Fprintln(os.Stderr, "unlocking and joining the network …")
	}
	client, err := bot.Start(context.Background(), opts)
	if err != nil {
		return err
	}
	defer client.Close()

	m := ui.New(client, store, client.Ready, ui.Options{Remote: remoteMode})
	p := tea.NewProgram(m)
	go func() {
		for e := range client.Events() {
			p.Send(ui.EventMsg{Event: e})
		}
		select {
		case <-client.Done():
		case <-time.After(2 * time.Second):
		}
		p.Send(ui.ExitMsg{Err: client.Err()})
	}()
	if _, err := p.Run(); err != nil {
		return err
	}
	if m.Err != nil {
		return m.Err
	}
	return nil
}

// askPassphrase reads the passphrase from the terminal without echo; for a
// new identity it asks twice. It never accepts one from the environment or
// the command line, where other processes or the shell history could see it.
func askPassphrase(confirm bool) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, errors.New("the passphrase is read from a terminal; run sotto in one")
	}
	read := func(prompt string) ([]byte, error) {
		fmt.Fprint(os.Stderr, prompt)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return b, err
	}
	if confirm {
		fmt.Fprintln(os.Stderr, "No identity on that machine yet; one will be created. Choose a passphrase.")
	}
	pass, err := read("Passphrase: ")
	if err != nil {
		return nil, err
	}
	if len(pass) == 0 {
		return nil, errors.New("the passphrase must not be empty")
	}
	if confirm {
		again, err := read("Again: ")
		same := err == nil && subtle.ConstantTimeCompare(pass, again) == 1
		clear(again)
		if !same {
			clear(pass)
			return nil, errors.New("the passphrases differ")
		}
	}
	return pass, nil
}
