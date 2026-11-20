# Security policy

sotto is a front end for `concord --bot`. Concord's own cryptography,
networking and threat model are Concord's; report problems there to that
project, through its
[security policy](https://github.com/ZahakJ/concord/blob/main/SECURITY.md).

Report here what this client adds:

- remote text reaching the terminal as a control sequence (any escape, C1
  control, or bidi override in a name, message, guild, channel or error);
- the passphrase leaving the process other than to concord's environment or,
  when attaching, down the transport's stdin, or being written anywhere;
- the verified mark appearing beside anyone whose fingerprint you have not
  verified;
- a name shown without a fingerprint tag when it matches yours or another
  member's after invisible characters are removed, or under the Unicode
  17.0 confusables data;
- the host daemon accepting an attach without the right passphrase, or being
  reachable by another user;
- the client opening or writing the Concord app's profile.

## How to report

Use GitHub's private vulnerability reporting: the *Security* tab, then
*Report a vulnerability*. Please do not open a public issue until a fix has
shipped. Say which version you tested (`sotto --version`) and how to
reproduce it. A failing test in the style of
`TestEscapeInjectionThroughEveryRemoteField` is ideal.

## Supported versions

Only the latest release. There are no backports.

## Known limits

- Go strings cannot be wiped. The passphrase is read into a byte slice and
  zeroed after use, but the copy placed in concord's environment is a string
  and lives until garbage collection. concord itself holds it for as long as
  it runs, and the same user can read it from `/proc/<pid>/environ`.
- The host daemon turns off core dumps and, on Linux, marks itself
  non-dumpable, so the same user cannot read its memory or environment
  through `/proc` or ptrace. On Linux and macOS it also refuses socket
  connections from any other uid; elsewhere it relies on the socket's
  directory being private (0700). The concord it runs is dumpable again
  after exec and holds the passphrase, as above.
- Look-alike detection is only as good as Unicode's confusables and
  identifier data (17.0.0, generated into `internal/safe/tables.go`). Two
  names can look identical in some font and still differ under those rules;
  such names are not tagged. Only the ✓ and the fingerprint identify a
  member.
- Verification marks are kept per device in `verified.json`. They are not
  shared with Concord or with your other devices.
- A host's daemon serves whoever can run commands as you on that machine and
  knows the passphrase. The daemon checks one passphrase at a time across all
  connections, at most one a second; after n wrong guesses in a row the next
  check waits 2^(n-1) seconds, up to five minutes, and a correct one resets
  this. At most four connections may wait to authenticate; more are refused.
  There is no lockout, and the backoff is lost when the daemon restarts.
  Someone who can already run commands as you can also delay your own attach
  by guessing.
