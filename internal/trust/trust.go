// Package trust remembers which fingerprints the user has verified.
//
// Verification is the user's own act: they compare a fingerprint with its
// owner out of band (in person, on a call) and type it back. The protocol
// carries fingerprints but no verification state, so the record lives here,
// in a small file beside the client's data, never in Concord's profile.
package trust

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Entry records one verification.
type Entry struct {
	Name string    `json:"name"` // the display name at the time, for the record
	At   time.Time `json:"at"`
}

// Store is the set of verified fingerprints, keyed by normalised form.
type Store struct {
	path string
	mu   sync.Mutex
	m    map[string]Entry
}

// Load reads the store at path; a missing file is an empty store.
func Load(path string) (*Store, error) {
	s := &Store{path: path, m: map[string]Entry{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.m); err != nil {
		return nil, fmt.Errorf("%s is unreadable (%v); move it aside to start over", path, err)
	}
	return s, nil
}

// Norm reduces a fingerprint to its 32 base32 characters, upper case.
func Norm(fp string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(fp) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// MatchFull reports whether typed is the whole of fp, ignoring case, spaces
// and punctuation. A prefix is not enough: verifying means comparing all of it.
func MatchFull(fp, typed string) bool {
	n := Norm(typed)
	return n != "" && n == Norm(fp) && len(n) >= 32
}

// Verified reports whether fp has been verified.
func (s *Store) Verified(fp string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[Norm(fp)]
	return ok
}

// Mark records fp as verified and saves.
func (s *Store) Mark(fp, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[Norm(fp)] = Entry{Name: name, At: time.Now().UTC().Truncate(time.Second)}
	return s.save()
}

// Unmark forgets fp and saves.
func (s *Store) Unmark(fp string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, Norm(fp))
	return s.save()
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".verified-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
