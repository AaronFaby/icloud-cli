package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aaronfaby/icloud-cli/internal/config"
	"github.com/aaronfaby/icloud-cli/internal/icloud/webdav"
	"github.com/aaronfaby/icloud-cli/internal/logging"
	"github.com/aaronfaby/icloud-cli/internal/output"
)

func TestServicesListJSON(t *testing.T) {
	t.Setenv(logging.EnvLog, logging.DestinationOff)
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

func TestServicesListIncludesResponseCommands(t *testing.T) {
	t.Setenv(logging.EnvLog, logging.DestinationOff)
	var stdout bytes.Buffer
	code := Run([]string{"services", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if code != output.ExitOK {
		t.Fatalf("exit code = %d, output = %s", code, stdout.String())
	}
	var env struct {
		Data []struct {
			Service    string   `json:"service"`
			Operations []string `json:"operations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var ops []string
	for _, service := range env.Data {
		if service.Service == "mail" {
			ops = service.Operations
			break
		}
	}
	for _, want := range []string{"messages:reply", "messages:reply-all", "messages:forward"} {
		var found bool
		for _, got := range ops {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("mail operations missing %q: %#v", want, ops)
		}
	}
}

func TestJSONFlagIsNoOpBeforeOrAfterCommand(t *testing.T) {
	t.Setenv(logging.EnvLog, logging.DestinationOff)
	for _, args := range [][]string{
		{"services", "list", "--json"},
		{"--json", "services", "list"},
	} {
		var stdout bytes.Buffer
		code := Run(args, strings.NewReader(""), &stdout, &bytes.Buffer{})
		if code != output.ExitOK {
			t.Fatalf("args %v exit code = %d, output = %s", args, code, stdout.String())
		}
		var env output.Envelope
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if !env.OK || env.Service != "services" || env.Operation != "list" {
			t.Fatalf("args %v unexpected envelope: %#v", args, env)
		}
	}
}

func TestNestedHelpExitsOKWithoutCredentials(t *testing.T) {
	t.Setenv(logging.EnvLog, logging.DestinationOff)
	t.Setenv("ICLOUD_APPLE_ID", "")
	t.Setenv("ICLOUD_APP_PASSWORD", "")
	var stdout bytes.Buffer
	code := Run([]string{"mail", "messages", "get", "--help"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if code != output.ExitOK {
		t.Fatalf("exit code = %d, output = %s", code, stdout.String())
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Usage string `json:"usage"`
			Flags []struct {
				Name string `json:"name"`
			} `json:"flags"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK || env.Data.Usage != "icloud mail messages get [flags]" {
		t.Fatalf("unexpected help envelope: %#v", env)
	}
	var sawID bool
	for _, flag := range env.Data.Flags {
		if flag.Name == "id" {
			sawID = true
		}
	}
	if !sawID {
		t.Fatalf("help flags missing id: %#v", env.Data.Flags)
	}
}

func TestResponseHelpIncludesDraftAndDryRun(t *testing.T) {
	t.Setenv(logging.EnvLog, logging.DestinationOff)
	var stdout bytes.Buffer
	code := Run([]string{"mail", "messages", "reply", "--help"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if code != output.ExitOK {
		t.Fatalf("exit code = %d, output = %s", code, stdout.String())
	}
	var env struct {
		Data struct {
			Flags []struct {
				Name string `json:"name"`
			} `json:"flags"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"draft", "dry-run", "input-json"} {
		var found bool
		for _, flag := range env.Data.Flags {
			if flag.Name == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("help flags missing %q: %#v", want, env.Data.Flags)
		}
	}
}

func TestMissingCredentialsUsesAuthExit(t *testing.T) {
	t.Setenv(logging.EnvLog, logging.DestinationOff)
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
	t.Setenv(logging.EnvLog, logging.DestinationOff)
	var stdout bytes.Buffer
	code := Run([]string{"notes", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if code != output.ExitUnsupported {
		t.Fatalf("exit code = %d, output = %s", code, stdout.String())
	}
}

func TestLogStatusJSON(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "icloud.log")
	t.Setenv(logging.EnvLog, logging.DestinationFile)
	t.Setenv(logging.EnvLogFile, logPath)
	t.Setenv(logging.EnvLogLevel, "info")
	t.Setenv(logging.EnvLogSize, "1")
	t.Setenv(logging.EnvLogNum, "0")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"log", "status"}, strings.NewReader(""), &stdout, &stderr)
	if code != output.ExitOK {
		t.Fatalf("exit code = %d, output = %s, stderr = %s", code, stdout.String(), stderr.String())
	}
	var env struct {
		OK        bool           `json:"ok"`
		Service   string         `json:"service"`
		Operation string         `json:"operation"`
		Data      logging.Status `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK || env.Service != "log" || env.Operation != "status" {
		t.Fatalf("unexpected envelope: %#v", env)
	}
	if !env.Data.Enabled || env.Data.Destination != logging.DestinationFile || env.Data.FilePath != logPath || env.Data.SizeMB != 1 || env.Data.History != 0 {
		t.Fatalf("unexpected status: %#v", env.Data)
	}
	if env.Data.ActiveFile == nil || !env.Data.ActiveFile.Exists {
		t.Fatalf("missing active file status: %#v", env.Data.ActiveFile)
	}
}

func TestStderrLoggingDoesNotBreakStdoutJSON(t *testing.T) {
	t.Setenv(logging.EnvLog, logging.DestinationStderr)
	t.Setenv(logging.EnvLogLevel, "info")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"services", "list"}, strings.NewReader(""), &stdout, &stderr)
	if code != output.ExitOK {
		t.Fatalf("exit code = %d, output = %s", code, stdout.String())
	}
	var env output.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "command_start") || !strings.Contains(stderr.String(), "command_success") {
		t.Fatalf("stderr missing lifecycle logs: %q", stderr.String())
	}
}

func TestLogOffCreatesNoFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "icloud.log")
	t.Setenv(logging.EnvLog, logging.DestinationOff)
	t.Setenv(logging.EnvLogFile, logPath)

	var stdout bytes.Buffer
	code := Run([]string{"services", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if code != output.ExitOK {
		t.Fatalf("exit code = %d, output = %s", code, stdout.String())
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("log file exists with logging off, err=%v", err)
	}
}

func TestInvalidLoggingEnvWarnsAndFallsBack(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "icloud.log")
	t.Setenv(logging.EnvLog, "nope")
	t.Setenv(logging.EnvLogFile, logPath)
	t.Setenv(logging.EnvLogSize, "bad")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"log", "status"}, strings.NewReader(""), &stdout, &stderr)
	if code != output.ExitOK {
		t.Fatalf("exit code = %d, output = %s, stderr = %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), logging.EnvLog) || !strings.Contains(stderr.String(), logging.EnvLogSize) {
		t.Fatalf("stderr missing logging warnings: %q", stderr.String())
	}
	var env struct {
		Data logging.Status `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Destination != logging.DestinationFile || env.Data.SizeMB != 10 || len(env.Data.Warnings) != 2 {
		t.Fatalf("unexpected status: %#v", env.Data)
	}
}

func TestBuildCalendarDataFromStructuredInput(t *testing.T) {
	got, err := buildCalendarData(eventInput{
		ID:          "planning-1",
		Summary:     "Planning, Review; Prep",
		Description: "line 1\nline 2",
		Location:    "HQ; Room, 2",
		Start:       "2026-06-09T10:00:00-07:00",
		End:         "2026-06-09T10:30:00-07:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"BEGIN:VCALENDAR\r\n",
		"UID:planning-1\r\n",
		"DTSTART:20260609T170000Z\r\n",
		"DTEND:20260609T173000Z\r\n",
		"SUMMARY:Planning\\, Review\\; Prep\r\n",
		"DESCRIPTION:line 1\\nline 2\r\n",
		"LOCATION:HQ\\; Room\\, 2\r\n",
		"END:VCALENDAR\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("calendar data missing %q in:\n%s", want, got)
		}
	}
}

func TestBuildCalendarDataNormalizesRawCalendarData(t *testing.T) {
	got, err := buildCalendarData(eventInput{CalendarData: "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:x\nEND:VEVENT\nEND:VCALENDAR\n\n"})
	if err != nil {
		t.Fatal(err)
	}
	want := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:x\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if got != want {
		t.Fatalf("calendar data = %q, want %q", got, want)
	}
}

func TestBuildCalendarDataRequiresStructuredFields(t *testing.T) {
	if _, err := buildCalendarData(eventInput{Start: "2026-06-09T10:00:00Z", End: "2026-06-09T10:30:00Z"}); err == nil {
		t.Fatal("expected missing summary error")
	}
	if _, err := buildCalendarData(eventInput{Summary: "Planning", Start: "2026-06-09T10:00:00Z"}); err == nil {
		t.Fatal("expected missing time error")
	}
}

func TestParseMailSince(t *testing.T) {
	now := func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	}
	tests := map[string]string{
		"24h":                  "13-Jun-2026",
		"2026-06-12":           "12-Jun-2026",
		"2026-06-12T23:00:00Z": "12-Jun-2026",
	}
	for input, want := range tests {
		got, err := parseMailSince(input, now)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("parseMailSince(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := parseMailSince("last week", now); err == nil {
		t.Fatal("expected invalid since error")
	}
}

func TestResolveCalendarName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Path {
		case "/.well-known/caldav":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/.well-known/caldav</D:href>
    <D:propstat><D:prop><D:current-user-principal><D:href>/principal/</D:href></D:current-user-principal></D:prop></D:propstat>
  </D:response>
</D:multistatus>`))
		case "/principal/":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:response>
    <D:href>/principal/</D:href>
    <D:propstat><D:prop><C:calendar-home-set><D:href>/calendars/</D:href></C:calendar-home-set></D:prop></D:propstat>
  </D:response>
</D:multistatus>`))
		case "/calendars/":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:">
  <D:response><D:href>/calendars/work/</D:href><D:propstat><D:prop><D:displayname>Work</D:displayname></D:prop></D:propstat></D:response>
  <D:response><D:href>/calendars/aristotle-a/</D:href><D:propstat><D:prop><D:displayname>Aristotle</D:displayname></D:prop></D:propstat></D:response>
  <D:response><D:href>/calendars/aristotle-b/</D:href><D:propstat><D:prop><D:displayname>aristotle</D:displayname></D:prop></D:propstat></D:response>
</D:multistatus>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := webdav.New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	href, err := resolveCalendarName(context.Background(), client, "work")
	if err != nil {
		t.Fatal(err)
	}
	if href != "/calendars/work/" {
		t.Fatalf("href = %q", href)
	}
	if _, err := resolveCalendarName(context.Background(), client, "Aristotle"); err == nil {
		t.Fatal("expected ambiguous calendar error")
	}
	if _, err := resolveCalendarName(context.Background(), client, "Missing"); err == nil {
		t.Fatal("expected missing calendar error")
	}
}

func TestBuildVCardFromStructuredInput(t *testing.T) {
	got, err := buildVCard(contactInput{
		ID:            "ada-1",
		GivenName:     "Ada",
		FamilyName:    "Lovelace",
		FormattedName: "Ada, Countess; Lovelace",
		Emails:        []string{"ada@example.com", "", "work@example.com"},
		Phones:        []string{"+1 555 0100"},
		Organization:  "Analytical Engines, Inc.; Research",
		Note:          "line 1\nline 2",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"BEGIN:VCARD\r\n",
		"VERSION:3.0\r\n",
		"UID:ada-1\r\n",
		"N:Lovelace;Ada;;;\r\n",
		"FN:Ada\\, Countess\\; Lovelace\r\n",
		"EMAIL;TYPE=INTERNET:ada@example.com\r\n",
		"EMAIL;TYPE=INTERNET:work@example.com\r\n",
		"TEL:+1 555 0100\r\n",
		"ORG:Analytical Engines\\, Inc.\\; Research\r\n",
		"NOTE:line 1\\nline 2\r\n",
		"END:VCARD\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("vcard missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "EMAIL;TYPE=INTERNET:\r\n") {
		t.Fatalf("vcard included empty email:\n%s", got)
	}
}

func TestBuildVCardUsesNameFallback(t *testing.T) {
	got, err := buildVCard(contactInput{
		UID:        "grace-1",
		GivenName:  "Grace",
		FamilyName: "Hopper",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "UID:grace-1\r\n") || !strings.Contains(got, "FN:Grace Hopper\r\n") {
		t.Fatalf("vcard = %s", got)
	}
}

func TestBuildVCardNormalizesRawVCard(t *testing.T) {
	got, err := buildVCard(contactInput{VCard: "BEGIN:VCARD\nVERSION:3.0\nUID:x\nFN:Ada\nEND:VCARD\n\n"})
	if err != nil {
		t.Fatal(err)
	}
	want := "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:x\r\nFN:Ada\r\nEND:VCARD\r\n"
	if got != want {
		t.Fatalf("vcard = %q, want %q", got, want)
	}
}

func TestBuildVCardRequiresName(t *testing.T) {
	if _, err := buildVCard(contactInput{Emails: []string{"ada@example.com"}}); err == nil {
		t.Fatal("expected missing contact name error")
	}
}
