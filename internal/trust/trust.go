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
	"syscall"
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
	m, err := read(path)
	if err != nil {
		return nil, err
	}
	return &Store{path: path, m: m}, nil
}

func read(path string) (map[string]Entry, error) {
	m := map[string]Entry{}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s is unreadable (%v); move it aside to start over", path, err)
	}
	return m, nil
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
	e := Entry{Name: name, At: time.Now().UTC().Truncate(time.Second)}
	return s.change(func(m map[string]Entry) { m[Norm(fp)] = e })
}

// Unmark forgets fp and saves.
func (s *Store) Unmark(fp string) error {
	return s.change(func(m map[string]Entry) { delete(m, Norm(fp)) })
}

// change applies fn to the file's current contents under an exclusive lock,
// so that another client sharing the file loses none of its changes.
func (s *Store) change(fn func(map[string]Entry)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close() // closing releases the lock
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	m, err := read(s.path)
	if err != nil {
		return err
	}
	fn(m)
	if err := save(s.path, m); err != nil {
		return err
	}
	s.m = m
	return nil
}

func save(path string, m map[string]Entry) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".verified-*")
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
	return os.Rename(tmp.Name(), path)
}
