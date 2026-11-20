package trust

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const fp = "ALIC EALI CEAL ICEA LICE ALIC EALI CEAL"

func TestMarkPersistsAndNormalises(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "verified.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Verified(fp) {
		t.Fatal("verified before marking")
	}
	if err := s.Mark("alic eali ceal icea lice alic eali ceal", "alice"); err != nil {
		t.Fatal(err)
	}
	s2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Verified(fp) || !s2.Verified("ALICEALICEALICEALICEALICEALICEAL") {
		t.Fatal("mark did not persist or normalise")
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v", st.Mode().Perm(), err)
	}
	if err := s2.Unmark(fp); err != nil {
		t.Fatal(err)
	}
	if s3, _ := Load(path); s3.Verified(fp) {
		t.Fatal("unmark did not persist")
	}
}

func TestMatchFull(t *testing.T) {
	if !MatchFull(fp, "alic-eali ceal icea lice alic eali ceal") {
		t.Fatal("full fingerprint with odd separators did not match")
	}
	for _, typed := range []string{"ALIC", "ALIC EALI CEAL ICEA LICE ALIC EALI CEAX", ""} {
		if MatchFull(fp, typed) {
			t.Fatalf("MatchFull accepted %q", typed)
		}
	}
}

func TestCorruptFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verified.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("corrupt store loaded silently")
	}
}

// Two clients on one device (say, one per terminal) share verified.json.
// Each change re-reads the file under a lock, so neither erases the other's.
func TestConcurrentStoresDoNotLoseMarks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "verified.json")
	a, _ := Load(p)
	b, _ := Load(p)
	fps := []string{
		"AAAA AAAA AAAA AAAA AAAA AAAA AAAA AAAA",
		"BBBB BBBB BBBB BBBB BBBB BBBB BBBB BBBB",
		"CCCC CCCC CCCC CCCC CCCC CCCC CCCC CCCC",
		"DDDD DDDD DDDD DDDD DDDD DDDD DDDD DDDD",
	}
	var wg sync.WaitGroup
	for i, fp := range fps {
		s := a
		if i%2 == 1 {
			s = b
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Mark(fp, "x"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := a.Unmark(fps[1]); err != nil { // b's mark, removed through a
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	for i, fp := range fps {
		if want := i != 1; c.Verified(fp) != want {
			t.Errorf("%s verified=%v, want %v", fp[:4], !want, want)
		}
	}
}
