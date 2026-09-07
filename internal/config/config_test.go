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
	t.Setenv(EnvAppleID, "")
	t.Setenv(EnvAppPassword, "")
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")
	_, _, err := RequireCredentials(path)
	if err == nil {
		t.Fatal("expected missing credential error")
	}
}

func TestSaveWritesConfig(t *testing.T) {
	t.Setenv(EnvAppleID, "")
	t.Setenv(EnvAppPassword, "")
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

func TestSaveReplacesPublicFilePrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(SaveOptions{Path: path, AppleID: "test@example.com", AppPassword: "test-password"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private replacement: info=%v err=%v", info, err)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".icloud-config-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary credentials left behind: %v %v", files, err)
	}
}

func TestLoadCompleteEnvDoesNotDependOnConfig(t *testing.T) {
	t.Setenv(EnvAppleID, "test@example.com")
	t.Setenv(EnvAppPassword, "test-password")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, report, err := RequireCredentials(path)
	if err != nil || cfg.AppleID != "test@example.com" || report.AppleID != "env" || report.AppPassword != "env" {
		t.Fatalf("environment credentials must take precedence: report=%+v err=%v", report, err)
	}
}

func TestFailedSaveCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing-directory")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(SaveOptions{Path: target, AppleID: "test@example.com", AppPassword: "test-password"}); err == nil {
		t.Fatal("expected failure replacing a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "existing-directory" {
		t.Fatalf("temporary credentials remain after failure: %v %v", entries, err)
	}
}
