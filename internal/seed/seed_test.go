package seed_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Sznapsollo/health-monitor-nj/internal/seed"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMissingCopiesOnlyWhatIsNotThere(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	write(t, filepath.Join(from, "example", "signals.yaml"), "shipped signals")
	write(t, filepath.Join(from, "example", "rules.yaml"), "shipped rules")
	write(t, filepath.Join(to, "example", "rules.yaml"), "edited rules")

	copied, err := seed.Missing(from, to)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(copied, []string{filepath.Join("example", "signals.yaml")}) {
		t.Errorf("copied = %v", copied)
	}
	if got, _ := os.ReadFile(filepath.Join(to, "example", "rules.yaml")); string(got) != "edited rules" {
		t.Errorf("an edited file was overwritten: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(to, "example", "signals.yaml")); string(got) != "shipped signals" {
		t.Errorf("signals.yaml = %q", got)
	}

	again, err := seed.Missing(from, to)
	if err != nil || len(again) != 0 {
		t.Errorf("second run copied %v, %v", again, err)
	}
}
