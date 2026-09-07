package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronfaby/icloud-cli/internal/config"
	"github.com/aaronfaby/icloud-cli/internal/logging"
)

func TestAuthSaveUsesEnvironmentWithoutExposingSecrets(t *testing.T) {
	t.Setenv(config.EnvAppleID, "private-account@example.invalid")
	t.Setenv(config.EnvAppPassword, "private-password-marker")
	t.Setenv(logging.EnvLog, logging.DestinationStderr)
	t.Setenv(logging.EnvLogLevel, "info")
	for _, tc := range []struct {
		name         string
		args         []string
		wantID       string
		wantPassword string
	}{
		{"environment", nil, "private-account@example.invalid", "private-password-marker"},
		{"mixed", []string{"--apple-id", "explicit@example.invalid"}, "explicit@example.invalid", "private-password-marker"},
		{"flags", []string{"--apple-id", "explicit@example.invalid", "--app-password", "explicit-password-marker"}, "explicit@example.invalid", "explicit-password-marker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			args := append([]string{"auth", "save", "--config", path}, tc.args...)
			var stdout, stderr bytes.Buffer
			if code := Run(append(append([]string{}, args...), "--help"), strings.NewReader(""), &stdout, &stderr); code != 0 {
				t.Fatalf("help failed: %d", code)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("help wrote credentials")
			}
			if code := Run(args, strings.NewReader(""), &stdout, &stderr); code != 0 {
				t.Fatalf("save failed: %d", code)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var saved config.Config
			if err := json.Unmarshal(data, &saved); err != nil || saved.AppleID != tc.wantID || saved.AppPassword != tc.wantPassword {
				t.Fatal("saved credentials do not match selected sources")
			}
			for _, secret := range []string{"private-account@example.invalid", "private-password-marker", "explicit@example.invalid", "explicit-password-marker"} {
				if strings.Contains(stdout.String()+stderr.String(), secret) {
					t.Fatal("credential leaked in help, output, or logs")
				}
			}
		})
	}
}
