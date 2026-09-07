package config

import (
	"os"
	"strings"
	"testing"
)

// writeTemp writes content to a temporary TOML file and returns its path.
// The file is cleaned up automatically when the test ends.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string // substring that must appear in the error; empty means success
	}{
		{
			name: "valid config",
			toml: `
path = "/etc/haproxy"
check_cmd = "haproxy -c -f /etc/haproxy"
reload_cmd = "systemctl reload haproxy.service"
`,
		},
		{
			name: "missing path",
			toml: `
check_cmd = "true"
reload_cmd = "true"
`,
			wantErr: "path is required",
		},
		{
			name: "missing check_cmd",
			toml: `
path = "/etc/haproxy"
reload_cmd = "true"
`,
			wantErr: "check_cmd is required",
		},
		{
			name: "missing reload_cmd",
			toml: `
path = "/etc/haproxy"
check_cmd = "true"
`,
			wantErr: "reload_cmd is required",
		},
		{
			name: "unknown field rejected",
			toml: `
path = "/etc/haproxy"
check_cmd = "true"
reload_cmd = "true"
bogus_field = "bad"
`,
			wantErr: "strict mode",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, tc.toml)
			cfg, err := Load(path)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (cfg=%+v)", tc.wantErr, cfg)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg == nil {
				t.Fatal("Load returned nil config with nil error")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(t.TempDir() + "/does-not-exist.toml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
