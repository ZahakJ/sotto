package paths

import (
	"path/filepath"
	"testing"
)

func TestBaseAndGuard(t *testing.T) {
	home := t.TempDir()
	env := []string{"HOME=" + home}
	base, err := Base(env)
	if err != nil || base != filepath.Join(home, ".local/share/concord-cli") {
		t.Fatalf("Base = %q, %v", base, err)
	}
	if err := Guard(Identity(base), env); err != nil {
		t.Fatalf("own identity refused: %v", err)
	}
	for _, bad := range []string{filepath.Join(home, ".config/concord"), filepath.Join(home, ".config/concord/x")} {
		if Guard(bad, env) == nil {
			t.Fatalf("GUI profile %s accepted", bad)
		}
	}
	xdg := filepath.Join(home, "cfg")
	if Guard(filepath.Join(xdg, "concord"), append(env, "XDG_CONFIG_HOME="+xdg)) == nil {
		t.Fatal("XDG GUI profile accepted")
	}
	if b, _ := Base(append(env, "XDG_DATA_HOME=/d")); b != "/d/concord-cli" {
		t.Fatalf("XDG base = %q", b)
	}
}
