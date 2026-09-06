package watch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMissing(t *testing.T) {
	for _, k := range []string{EnvWatchPath, EnvCheckCmd, EnvReloadCmd, EnvStateDir} {
		t.Setenv(k, "")
	}

	_, err := LoadConfig(Config{})
	if err == nil {
		t.Fatal("expected error for missing settings, got nil")
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv(EnvWatchPath, "/tmp/watched")
	t.Setenv(EnvCheckCmd, "true")
	t.Setenv(EnvReloadCmd, "true")
	t.Setenv(EnvStateDir, "/tmp/state")

	cfg, err := LoadConfig(Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Config{WatchPath: "/tmp/watched", StateDir: "/tmp/state", CheckCmd: "true", ReloadCmd: "true"}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadConfigFromFlags(t *testing.T) {
	for _, k := range []string{EnvWatchPath, EnvCheckCmd, EnvReloadCmd, EnvStateDir} {
		t.Setenv(k, "")
	}

	cfg, err := LoadConfig(Config{
		WatchPath: "/tmp/watched",
		StateDir:  "/tmp/state",
		CheckCmd:  "true",
		ReloadCmd: "true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Config{WatchPath: "/tmp/watched", StateDir: "/tmp/state", CheckCmd: "true", ReloadCmd: "true"}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadConfigFlagWinsOverEnv(t *testing.T) {
	t.Setenv(EnvWatchPath, "/env/watched")
	t.Setenv(EnvCheckCmd, "/env/check")
	t.Setenv(EnvReloadCmd, "/env/reload")
	t.Setenv(EnvStateDir, "/env/state")

	// Only WatchPath is overridden by a flag; the rest must still fall
	// back to the environment.
	cfg, err := LoadConfig(Config{WatchPath: "/flag/watched"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Config{WatchPath: "/flag/watched", StateDir: "/env/state", CheckCmd: "/env/check", ReloadCmd: "/env/reload"}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestRunFirstRunAdoptsWithoutReload(t *testing.T) {
	dir := t.TempDir()
	watchPath := filepath.Join(dir, "config")
	if err := os.WriteFile(watchPath, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")

	cfg := Config{
		WatchPath: watchPath,
		StateDir:  stateDir,
		CheckCmd:  "true",
		ReloadCmd: "false", // must not run on a first-run adopt
	}

	if err := Run(cfg); err != nil {
		t.Fatalf("first run: %v", err)
	}

	stored, err := readState(filepath.Join(stateDir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if stored == "" {
		t.Fatal("expected state to be recorded after first run")
	}
}

func TestRunNoChangeIsNoop(t *testing.T) {
	dir := t.TempDir()
	watchPath := filepath.Join(dir, "config")
	if err := os.WriteFile(watchPath, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")

	cfg := Config{WatchPath: watchPath, StateDir: stateDir, CheckCmd: "true", ReloadCmd: "true"}
	if err := Run(cfg); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Second run, unchanged content: reload/check must not fire, so make
	// them fail loudly if they do.
	cfg.CheckCmd = "false"
	cfg.ReloadCmd = "false"
	if err := Run(cfg); err != nil {
		t.Fatalf("unexpected error on unchanged content: %v", err)
	}
}

func TestRunChangedContentReloads(t *testing.T) {
	dir := t.TempDir()
	watchPath := filepath.Join(dir, "config")
	if err := os.WriteFile(watchPath, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")

	cfg := Config{WatchPath: watchPath, StateDir: stateDir, CheckCmd: "true", ReloadCmd: "true"}
	if err := Run(cfg); err != nil {
		t.Fatalf("first run: %v", err)
	}

	if err := os.WriteFile(watchPath, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(dir, "reloaded")
	cfg.ReloadCmd = "touch " + marker
	if err := Run(cfg); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("expected reload command to have run")
	}
}

func TestRunCheckFailureSkipsReload(t *testing.T) {
	dir := t.TempDir()
	watchPath := filepath.Join(dir, "config")
	if err := os.WriteFile(watchPath, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(dir, "state")

	cfg := Config{WatchPath: watchPath, StateDir: stateDir, CheckCmd: "true", ReloadCmd: "true"}
	if err := Run(cfg); err != nil {
		t.Fatalf("first run: %v", err)
	}

	if err := os.WriteFile(watchPath, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(dir, "reloaded")
	cfg.CheckCmd = "false"
	cfg.ReloadCmd = "touch " + marker
	if err := Run(cfg); err == nil {
		t.Fatal("expected error when check command fails")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("reload must not run when check fails")
	}

	// State must not have advanced, so a later fix-and-rerun reloads.
	stored, err := readState(filepath.Join(stateDir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	current, err := hashPath(watchPath)
	if err != nil {
		t.Fatal(err)
	}
	if stored == current {
		t.Fatal("state must not advance when the check command fails")
	}
}

func TestHashPathFileAndDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirHash, err := hashPath(dir)
	if err != nil {
		t.Fatal(err)
	}

	fileHash, err := hashPath(filepath.Join(dir, "a"))
	if err != nil {
		t.Fatal(err)
	}

	if dirHash == fileHash {
		t.Fatal("directory hash should differ from single-file hash")
	}

	// Hashing is deterministic and order-independent of readdir order.
	dirHash2, err := hashPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirHash != dirHash2 {
		t.Fatal("hashPath must be deterministic")
	}
}

func TestHashPathEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if _, err := hashPath(dir); err == nil {
		t.Fatal("expected error for a directory with no regular files")
	}
}
