package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPrefersEnvOverConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"apple_id":"file@example.com","app_password":"file-pass"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvAppleID, "env@example.com")
	t.Setenv(EnvAppPassword, "env-pass")

	cfg, report, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppleID != "env@example.com" || cfg.AppPassword != "env-pass" {
		t.Fatalf("env credentials did not win: %#v", cfg)
	}
	if report.AppleID != "env" || report.AppPassword != "env" {
		t.Fatalf("unexpected source report: %#v", report)
	}
}

func TestRequireCredentialsReportsMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")
	_, _, err := RequireCredentials(path)
	if err == nil {
		t.Fatal("expected missing credential error")
	}
}

func TestSaveWritesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	saved, err := Save(SaveOptions{Path: path, AppleID: "me@example.com", AppPassword: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	if saved != path {
		t.Fatalf("saved path = %q, want %q", saved, path)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppleID != "me@example.com" || cfg.AppPassword != "pass" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}
