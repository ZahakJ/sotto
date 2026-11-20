// Package paths decides where sotto keeps things. Its identity is its
// own: a separate CONCORD_HOME, never the desktop app's profile.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Base is the client's data directory: $XDG_DATA_HOME/sotto, or
// ~/.local/share/sotto.
func Base(env []string) (string, error) {
	if d := get(env, "XDG_DATA_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "sotto"), nil
	}
	home := get(env, "HOME")
	if home == "" {
		return "", errors.New("neither $XDG_DATA_HOME nor $HOME is set; pass --home")
	}
	return filepath.Join(home, ".local", "share", "sotto"), nil
}

// Identity is the CONCORD_HOME given to concord.
func Identity(base string) string { return filepath.Join(base, "identity") }

// Trust is the file of verified fingerprints.
func Trust(base string) string { return filepath.Join(base, "verified.json") }

// Sock is where a host listens for attaching clients.
func Sock(base string) string { return filepath.Join(base, "host.sock") }

// GUIProfiles are the directories the Concord app uses by default.
func GUIProfiles(env []string) []string {
	var out []string
	if d := get(env, "XDG_CONFIG_HOME"); d != "" {
		out = append(out, filepath.Join(d, "concord"))
	}
	if h := get(env, "HOME"); h != "" {
		out = append(out, filepath.Join(h, ".config", "concord"), filepath.Join(h, "Library", "Application Support", "concord"))
	}
	return out
}

// Guard refuses an identity directory that is, or is inside, a GUI profile:
// two processes on one profile corrupt it, and the GUI's identity is not
// this client's to unlock.
func Guard(identity string, env []string) error {
	id := resolve(identity)
	for _, g := range GUIProfiles(env) {
		g = resolve(g)
		if id == g || strings.HasPrefix(id, g+string(filepath.Separator)) {
			return fmt.Errorf("refusing to use %s: it is the Concord app's profile; sotto keeps its own identity (see --home)", identity)
		}
	}
	return nil
}

func resolve(p string) string {
	p, _ = filepath.Abs(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func get(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], key+"="); ok {
			return v
		}
	}
	return ""
}

// Fresh reports whether dir has no identity yet (missing or empty), so the
// passphrase should be asked for twice.
func Fresh(dir string) bool {
	ents, err := os.ReadDir(dir)
	return err != nil || len(ents) == 0
}
