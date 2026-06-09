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
