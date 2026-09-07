package watch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveStateDirMissing(t *testing.T) {
	t.Setenv(EnvStateDir, "")

	_, err := ResolveStateDir("")
	if err == nil {
		t.Fatal("expected error for missing state dir, got nil")
	}
}

func TestResolveStateDirFromEnv(t *testing.T) {
	t.Setenv(EnvStateDir, "/tmp/state")

	dir, err := ResolveStateDir("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/tmp/state" {
		t.Fatalf("got %q, want %q", dir, "/tmp/state")
	}
}

func TestResolveStateDirFlagWinsOverEnv(t *testing.T) {
	t.Setenv(EnvStateDir, "/env/state")

	dir, err := ResolveStateDir("/flag/state")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dir != "/flag/state" {
		t.Fatalf("got %q, want %q", dir, "/flag/state")
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

func TestHashPathFollowsSymlinkRoot(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "release-1")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "app.conf"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	current := filepath.Join(dir, "current")
	if err := os.Symlink(real, current); err != nil {
		t.Fatal(err)
	}

	hash1, err := hashPath(current)
	if err != nil {
		t.Fatalf("hashing symlinked directory: %v", err)
	}

	// Atomic update: point the symlink at a new release rather than
	// editing content in place.
	next := filepath.Join(dir, "release-2")
	if err := os.Mkdir(next, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(next, "app.conf"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(next, current); err != nil {
		t.Fatal(err)
	}

	hash2, err := hashPath(current)
	if err != nil {
		t.Fatalf("hashing after symlink swap: %v", err)
	}
	if hash1 == hash2 {
		t.Fatal("expected hash to change after swapping the watched symlink to new content")
	}
}
