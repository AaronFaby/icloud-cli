package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	clearEnv(t)

	cfg := LoadConfig()
	if cfg.Destination != DestinationFile || cfg.Level != "warn" || cfg.SizeMB != 10 || cfg.History != 3 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.FilePath == "" || !strings.HasSuffix(cfg.FilePath, filepath.Join("icloud-cli", "icloud.log")) {
		t.Fatalf("unexpected default file path: %q", cfg.FilePath)
	}
}

func TestLoadConfigInvalidValuesFallbackWithWarnings(t *testing.T) {
	clearEnv(t)
	t.Setenv(EnvLog, "bogus")
	t.Setenv(EnvLogLevel, "debug")
	t.Setenv(EnvLogSize, "large")
	t.Setenv(EnvLogNum, "-1")

	cfg := LoadConfig()
	if cfg.Destination != DestinationFile || cfg.Level != "warn" || cfg.SizeMB != 10 || cfg.History != 3 {
		t.Fatalf("unexpected fallback config: %#v", cfg)
	}
	if len(cfg.Warnings) != 4 {
		t.Fatalf("warning count = %d, want 4: %#v", len(cfg.Warnings), cfg.Warnings)
	}
}

func TestConfigureOffStatus(t *testing.T) {
	clearEnv(t)
	var stderr bytes.Buffer
	cfg := Configure(Config{Destination: DestinationOff, Level: "info", SizeMB: 10, History: 3}, &stderr)
	if len(cfg.Warnings) != 0 || stderr.Len() != 0 {
		t.Fatalf("unexpected warnings: cfg=%#v stderr=%q", cfg, stderr.String())
	}

	status := CurrentStatus()
	if status.Enabled || status.Destination != DestinationOff || status.ActiveFile != nil {
		t.Fatalf("unexpected off status: %#v", status)
	}
}

func TestConfigureInvalidWarningsGoToStderr(t *testing.T) {
	clearEnv(t)
	var stderr bytes.Buffer
	Configure(Config{
		Destination: DestinationOff,
		Level:       "warn",
		SizeMB:      10,
		History:     3,
		Warnings:    []string{"bad setting"},
	}, &stderr)
	if !strings.Contains(stderr.String(), "bad setting") {
		t.Fatalf("stderr missing off warning: %q", stderr.String())
	}

	stderr.Reset()
	Configure(Config{
		Destination: DestinationStderr,
		Level:       "warn",
		SizeMB:      10,
		History:     3,
		Warnings:    []string{"bad setting"},
	}, &stderr)
	if !strings.Contains(stderr.String(), "bad setting") {
		t.Fatalf("stderr missing warning: %q", stderr.String())
	}
}

func TestConfigureFileFailureFallsBackToStderr(t *testing.T) {
	clearEnv(t)
	logPath := filepath.Join(t.TempDir(), "not-a-file")
	if err := os.Mkdir(logPath, 0o700); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cfg := Configure(Config{
		Destination: DestinationFile,
		Level:       "warn",
		FilePath:    logPath,
		SizeMB:      10,
		History:     3,
	}, &stderr)
	if cfg.Destination != DestinationStderr {
		t.Fatalf("destination = %q, want stderr", cfg.Destination)
	}
	if !strings.Contains(stderr.String(), "failed to open log file") {
		t.Fatalf("stderr missing file failure warning: %q", stderr.String())
	}
}

func TestRotatingWriterPreservesHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "icloud.log")
	w, err := newRotatingWriter(path, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("1234567890")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("missing first rotated file: %v", err)
	}
	if _, err := w.Write([]byte("defghijklm")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("xyz")); err != nil {
		t.Fatal(err)
	}
	if got := countRotated(path, 2); got != 2 {
		t.Fatalf("rotated count = %d, want 2", got)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third history file err=%v", err)
	}
}

func TestRotatingWriterHistoryZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "icloud.log")
	w, err := newRotatingWriter(path, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if got := countRotated(path, 3); got != 0 {
		t.Fatalf("rotated count = %d, want 0", got)
	}
}

func TestSanitizers(t *testing.T) {
	args := SanitizedArgs([]string{
		"auth", "save",
		"--apple-id", "person@example.com",
		"--app-password=secret",
		"--input-json", `{"subject":"private","text":"secret"}`,
		"--folder", "INBOX",
	})
	want := []string{"auth", "save", "--apple-id", "[redacted]", "--app-password=[redacted]", "--input-json", "[redacted]", "--folder", "INBOX"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
	if got := SanitizedArgs([]string{`--input-json={"subject":"private"}`}); got[0] != "--input-json=[json]" {
		t.Fatalf("inline input json not redacted: %#v", got)
	}
	if got := SanitizedIMAPCommand(`LOGIN "person@example.com" "secret"`); got != "LOGIN [redacted]" {
		t.Fatalf("imap sanitizer = %q", got)
	}
	if got := SanitizedSMTPCommand("AUTH PLAIN abc123"); got != "AUTH [redacted]" {
		t.Fatalf("smtp sanitizer = %q", got)
	}
	if got := SanitizedURL("https://example.com/path?token=secret"); got != "https://example.com/path" {
		t.Fatalf("url sanitizer = %q", got)
	}
	if got := SanitizedArgs([]string{"--header", "Authorization: Basic abc"}); got[1] != "[redacted]" {
		t.Fatalf("authorization not redacted: %#v", got)
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{EnvLog, EnvLogLevel, EnvLogFile, EnvLogSize, EnvLogNum} {
		t.Setenv(key, "")
	}
}
