package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aaronfaby/icloud-cli/internal/output"
)

func TestServicesListJSON(t *testing.T) {
	var stdout bytes.Buffer
	code := Run([]string{"services", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if code != output.ExitOK {
		t.Fatalf("exit code = %d, output = %s", code, stdout.String())
	}
	var env output.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK || env.Service != "services" || env.Operation != "list" {
		t.Fatalf("unexpected envelope: %#v", env)
	}
}

func TestMissingCredentialsUsesAuthExit(t *testing.T) {
	t.Setenv("ICLOUD_APPLE_ID", "")
	t.Setenv("ICLOUD_APP_PASSWORD", "")
	t.Setenv("ICLOUD_CONFIG", t.TempDir()+"/missing.json")
	var stdout bytes.Buffer
	code := Run([]string{"auth", "check"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if code != output.ExitAuth {
		t.Fatalf("exit code = %d, output = %s", code, stdout.String())
	}
	var env output.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.OK || env.Error == nil || env.Error.Code != "missing_credentials" {
		t.Fatalf("unexpected envelope: %#v", env)
	}
}

func TestUnsupportedServiceExit(t *testing.T) {
	var stdout bytes.Buffer
	code := Run([]string{"notes", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if code != output.ExitUnsupported {
		t.Fatalf("exit code = %d, output = %s", code, stdout.String())
	}
}
