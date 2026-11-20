# concord-cli

A small terminal client for [Concord](https://github.com/ZahakJ/concord), the
serverless, end-to-end encrypted chat app. It drives the headless
`concord --bot` over its documented line protocol and adds nothing to the
cryptography: it is a front end, and a peer like any other.

```
 concord              │ # general  E2E                   2 members · 1 verified
                      │
 E2E                  │
▌ # general           │
                      │
 direct               │
  @ Notes             │
                      │────────────────────── Thu 19 Nov ──────────────────────
                      │20:33 bob  hello from bob
                      │20:33 alice ✓  evil^[[2J^[]0;owned^G^[]52;c;cm0gLXJmIH4=^
                      │               G�6n^Mdone
                      │20:33 alice ✓  and hello back from alice
 bob                  │─ verified alice ────────────────────────────────────────
 JVQK ZWZT            │› message #general
```

This is a real 80-column capture from the end-to-end test, trimmed in height.
The second message tried to clear the screen, retitle the window, write the
clipboard and send a cursor query. Here it is inert text.

## Install

You need the headless `concord` (`make cli` in the Concord repository, or a
release) and Go 1.26 or later.

```sh
go install github.com/ZahakJ/concord-cli@latest
concord-cli
```

The first run creates an identity of its own in
`~/.local/share/concord-cli/identity`. It never opens the desktop app's
profile, and it refuses to if pointed at it. Join a guild with an invite from
someone in it: `/join <code>`.

## Use

| Key | |
|---|---|
| `ctrl-k` | switch channel, by typing part of its name |
| `alt-↑` `alt-↓` | previous, next channel |
| `tab` | members, with full fingerprints |
| `pgup` `pgdn` | scroll |
| `ctrl-c` | clear the line, then quit |

| Command | |
|---|---|
| `/join <code>` | join a guild by invite |
| `/verify <name>` | show a member's fingerprint to compare with them |
| `/verify <fingerprint>` | mark it verified once it matches; ✓ marks that member from then on |
| `/name <name>` | set your display name |
| `/whoami` | your own fingerprint |
| `/help` | the rest |

Names are self-chosen, so two members with one name are shown with a
fingerprint tag (`alice·MALL`). Verifying means typing the whole fingerprint
the other person reads out to you; a name alone verifies nothing.

## Attach from another machine

Keep the identity on one machine and chat from any other that can reach it
through a transport you already have:

```sh
concord-cli --via 'ssh box'               # or: --via 'jaunt run box --'
```

The transport runs `concord-cli host` on `box`. The first attach starts a
small daemon there that keeps `concord --bot` running after you leave and
remembers, in memory, the last 500 events. The next attach, from this device
or another, replays them, and the daemon forgets them only once the client
confirms it has them. If more than 500 arrived, the client says how many
were dropped. `/quit` detaches; `/shutdown` stops the daemon.

## Design notes

**Protocol.** The client speaks bot protocol v1, as documented in Concord's
`docs/BOTS.md` since v0.59.0. It checks `concord --version` and `--help`
before starting and refuses an older binary. A `ready` event that announces a
newer protocol is refused too. It imports nothing from Concord.

**What v1 cannot do.** There are no commands to create a guild or an invite,
to read history, or to read Concord's own verification state. Guilds and
invites are made in the app. Messages appear from the moment the client
starts. Verification is recorded by this client in `verified.json`, on the
device where you made it.

**Escapes.** Every string a peer controls (names, messages, guild and channel
names, and errors that quote them) is cleaned once, where the protocol is
decoded. Control characters are shown in caret form (`^[`), C1 controls as
U+FFFD, and bidirectional overrides are removed. Nothing remote can reach the
terminal as a control sequence. Tests feed hostile input through every field
and check every rendered frame.

**Attach over a mesh.** Attaching is built for a mesh such as jaunt, where
`jaunt run <device> -- <command>` runs a command on another of your devices
over the mesh's authenticated, encrypted session, with stdin and stdout
carried across. That is exactly what `--via` needs. It maps as follows:

- *No new port.* The host listens only on a Unix socket in its data
  directory, mode 0600. Every byte between devices travels inside the mesh
  session that jaunt already authenticates.
- *Passphrase.* It is typed on the device you are at and sent as the first
  line of the session. It never appears in an argument or an environment
  variable on the remote side, and is never written to disk there. The
  daemon keeps a keyed hash to check later attaches, and gives the
  passphrase to `concord` in its environment, the only way concord accepts
  one.
- *One identity, many devices.* The newest attach takes over and earlier ones
  are detached. Verification marks stay on each device.
- *Who can attach.* Anyone the mesh lets run commands as you on the host, and
  who knows the passphrase.

Nothing here depends on jaunt itself. Any stdio transport works, and the
test suite uses `env -i` as one, which also proves that the passphrase does
not travel in the environment.

## Develop

```sh
make test       # unit tests with -race, against a fake concord process
make vuln       # govulncheck
make e2e CONCORD_SRC=../concord   # two real peers on this machine
```

The end-to-end test builds a real `concord`, gives each peer a throwaway
`HOME`, `XDG_CONFIG_HOME` and `CONCORD_HOME`, and runs both peers on this
machine. Alice creates a guild with concord's own REPL. Bob joins through
concord-cli. Alice then attaches through the host, and the two exchange
messages in both directions, hostile text included. Alice detaches and
reattaches to collect what she missed, then shuts the host down. The test
finishes by checking that no process is left running.

## Licence

GNU Affero General Public License, version 3 or later, as Concord. See
[LICENSE](LICENSE).
