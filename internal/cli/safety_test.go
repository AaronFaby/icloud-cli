package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aaronfaby/icloud-cli/internal/logging"
	"github.com/aaronfaby/icloud-cli/internal/output"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUnexpectedArgumentsAreValidationFailures(t *testing.T) {
	t.Setenv(logging.EnvLog, logging.DestinationOff)
	for _, args := range [][]string{
		{"mail", "messages", "delete", "--id", "123", "unexpected", "--dry-run"},
		{"services", "list", "unexpected"},
		{"services", "list", "--unknown"},
		{"mail", "messages", "list", "--limit", "invalid"},
		{"mail", "batch", "unknown", "--input-json", `{"dry_run":true,"ids":["123"]}`},
		{"calendar", "events", "update", "--calendar", "/calendar/", "--input-json", `{"summary":"Title","start":"20260101T010000Z","end":"20260101T020000Z"}`},
		{"contacts", "contacts", "update", "--book", "/book/", "--input-json", `{"formatted_name":"Name"}`},
	} {
		var stdout bytes.Buffer
		if code := Run(args, strings.NewReader(""), &stdout, &bytes.Buffer{}); code != output.ExitValidation {
			t.Fatalf("args=%v code=%d output=%s", args, code, &stdout)
		}
	}
}

func TestCreateUniquenessAndStructuredUpdateIdentity(t *testing.T) {
	t.Setenv(logging.EnvLog, logging.DestinationOff)
	t.Setenv("ICLOUD_APPLE_ID", "test@example.invalid")
	t.Setenv("ICLOUD_APP_PASSWORD", "dummy")
	t.Setenv("ICLOUD_CONFIG", t.TempDir()+"/missing.json")
	oldTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	for _, kind := range []string{"event", "contact"} {
		t.Run(kind, func(t *testing.T) {
			prefix := []string{"calendar", "events"}
			collectionFlag := "--calendar"
			extension := ".ics"
			payload := map[string]string{"summary": "Title", "start": "20260101T010000Z", "end": "20260101T020000Z"}
			if kind == "contact" {
				prefix = []string{"contacts", "contacts"}
				collectionFlag = "--book"
				extension = ".vcf"
				payload = map[string]string{"formatted_name": "Name"}
			}
			records := map[string]string{"/records/arbitrary-filename" + extension: "BEGIN:VCARD\r\nUID;VALUE=text:stored-\r\n identity\r\nFN:Old\r\nEND:VCARD\r\n"}
			if kind == "event" {
				records["/records/arbitrary-filename"+extension] = "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:stored-\r\n identity\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
			}
			gets, puts := 0, 0
			http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				response := &http.Response{StatusCode: 204, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}
				switch r.Method {
				case "GET":
					gets++
					response.StatusCode = 200
					response.Header.Set("ETag", `"read-version"`)
					response.Body = io.NopCloser(strings.NewReader(records[r.URL.Path]))
				case "PUT":
					puts++
					data, _ := io.ReadAll(r.Body)
					if r.Header.Get("If-None-Match") == "*" {
						if _, exists := records[r.URL.Path]; exists {
							response.StatusCode = 412
							return response, nil
						}
					} else if r.Header.Get("If-Match") != `"read-version"` {
						t.Fatalf("update lost exact ETag: %v", r.Header)
					}
					records[r.URL.Path] = string(data)
				default:
					t.Fatalf("unexpected request %s", r.Method)
				}
				return response, nil
			})
			run := func(op, id string) int {
				args := append(append([]string{}, prefix...), op, collectionFlag, "/records/")
				if id != "" {
					args = append(args, "--id", id)
				}
				b, _ := json.Marshal(payload)
				args = append(args, "--input-json", string(b))
				var stdout bytes.Buffer
				return Run(args, strings.NewReader(""), &stdout, &bytes.Buffer{})
			}
			if code := run("create", ""); code != 0 {
				t.Fatalf("first create code=%d", code)
			}
			if code := run("create", ""); code != 0 || len(records) != 3 {
				t.Fatalf("create collision: code=%d count=%d", code, len(records))
			}
			if code := run("create", "arbitrary-filename"); code != output.ExitRemote {
				t.Fatalf("explicit existing ID create code=%d", code)
			}
			if code := run("update", "arbitrary-filename"); code != 0 {
				t.Fatalf("update code=%d", code)
			}
			if got := records["/records/arbitrary-filename"+extension]; !strings.Contains(got, "UID:stored-identity\r\n") {
				t.Fatalf("update changed stored UID: %s", got)
			}
			payload["uid"] = "different-identity"
			if code := run("update", "arbitrary-filename"); code != output.ExitValidation {
				t.Fatalf("mismatched UID accepted: %d", code)
			}
			if gets != 2 || puts != 4 {
				t.Fatalf("GET=%d PUT=%d", gets, puts)
			}
		})
	}
}

func TestStructuredDatesAndUIDEscaping(t *testing.T) {
	for _, start := range []string{"nonsense", "2026-02-30T01:00:00Z", "20260101T030000Z"} {
		if _, err := buildCalendarData(eventInput{Summary: "Title", Start: start, End: "20260101T020000Z"}); err == nil {
			t.Fatalf("accepted start %q", start)
		}
	}
	uid, err := preservedUID("BEGIN:VCARD\r\nUID:value\\,semi\\;slash\\\\x\r\nEND:VCARD\r\n", "")
	if err != nil || uid != `value,semi;slash\x` {
		t.Fatalf("UID=%q err=%v", uid, err)
	}
	if _, err := preservedUID("BEGIN:VCARD\nFN:No UID\nEND:VCARD", ""); err == nil {
		t.Fatal("missing UID accepted")
	}
}

func TestLifecycleClassificationDoesNotLogArbitraryPositionals(t *testing.T) {
	for _, args := range [][]string{{"private-user-marker"}, {"services", "list", "private-subject-marker"}, {"mail", "messages", "get", "private-body-marker"}} {
		service, operation := classify(args)
		if strings.Contains(service, "private-") || strings.Contains(operation, "private-") {
			t.Fatalf("unsafe lifecycle labels %s %s", service, operation)
		}
	}
}

func TestJSONNoOpDoesNotConsumeFlagValues(t *testing.T) {
	for _, tc := range []struct{ args, want []string }{
		{[]string{"--json", "mail", "folders", "create", "--name", "--json"}, []string{"mail", "folders", "create", "--name", "--json"}},
		{[]string{"mail", "messages", "send", "--input-json", "--json"}, []string{"mail", "messages", "send", "--input-json", "--json"}},
		{[]string{"mail", "messages", "get", "--raw", "--json", "--id", "123"}, []string{"mail", "messages", "get", "--raw", "--id", "123"}},
		{[]string{"services", "list", "--json=false"}, []string{"services", "list"}},
		{[]string{"services", "list", "--", "--json"}, []string{"services", "list", "--", "--json"}},
	} {
		if got := normalizeArgs(tc.args); strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
			t.Fatalf("normalizeArgs(%q)=%q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestStructuredUpdateKeepsUIDWhitespace(t *testing.T) {
	uid, err := preservedUID("BEGIN:VCARD\r\nUID:  existing-identity \r\nEND:VCARD\r\n", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := buildVCard(contactInput{UID: uid, FormattedName: "Name"})
	if err != nil || !strings.Contains(card, "UID:  existing-identity \r\n") {
		t.Fatalf("vCard changed UID: %q err=%v", card, err)
	}
	event, err := buildCalendarData(eventInput{UID: uid, Summary: "Title", Start: "20260101T010000Z", End: "20260101T020000Z"})
	if err != nil || !strings.Contains(event, "UID:  existing-identity \r\n") {
		t.Fatalf("event changed UID: %q err=%v", event, err)
	}
}

func TestAllGroupHelpIsOfflineAndPreservesLeafValues(t *testing.T) {
	t.Setenv(logging.EnvLog, logging.DestinationOff)
	t.Setenv("ICLOUD_APPLE_ID", "")
	t.Setenv("ICLOUD_APP_PASSWORD", "")
	t.Setenv("ICLOUD_CONFIG", t.TempDir()+"/missing.json")
	for _, group := range []string{"auth", "services", "log", "mail", "mail folders", "mail messages", "mail messages attachment", "mail batch", "calendar", "calendar calendars", "calendar events", "contacts", "contacts books", "contacts contacts"} {
		for _, help := range []string{"--help", "-h"} {
			args := append(strings.Fields(group), help, "--json")
			var stdout bytes.Buffer
			if code := Run(args, strings.NewReader(""), &stdout, &bytes.Buffer{}); code != output.ExitOK {
				t.Fatalf("%v code=%d output=%s", args, code, &stdout)
			}
			var env struct {
				Data struct {
					Commands []string `json:"commands"`
				}
			}
			if err := json.Unmarshal(stdout.Bytes(), &env); err != nil || len(env.Data.Commands) == 0 {
				t.Fatalf("missing group help: %s err=%v", &stdout, err)
			}
		}
	}
	var stdout bytes.Buffer
	if code := Run([]string{"mail", "messages", "get", "--body", "--help"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); code != output.ExitValidation {
		t.Fatalf("leaf string flag treated as help: code=%d output=%s", code, &stdout)
	}
}
