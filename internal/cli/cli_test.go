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
