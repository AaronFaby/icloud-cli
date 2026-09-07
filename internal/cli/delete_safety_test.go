package cli

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aaronfaby/icloud-cli/internal/logging"
	"github.com/aaronfaby/icloud-cli/internal/output"
)

func TestCLIResourceDeleteValidatesTypeBeforeMutation(t *testing.T) {
	t.Setenv(logging.EnvLog, logging.DestinationOff)
	t.Setenv("ICLOUD_APPLE_ID", "dummy")
	t.Setenv("ICLOUD_APP_PASSWORD", "dummy-secret")
	t.Setenv("ICLOUD_CONFIG", t.TempDir()+"/missing.json")
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, tc := range []struct {
		name, service, group, parentFlag, target, resourceType, contentType string
		valid                                                               bool
	}{
		{"calendar collection", "calendar", "events", "--calendar", "https://p01-caldav.icloud.com/123/calendars/victim/", "<D:collection/>", "text/calendar", false},
		{"event cannot delete contact", "calendar", "events", "--calendar", "https://contacts.icloud.com/123/carddavhome/card/contact.vcf", "", "text/vcard", false},
		{"contact cannot delete event", "contacts", "contacts", "--book", "https://p01-caldav.icloud.com/123/calendars/work/event.ics", "", "text/calendar", false},
		{"address book collection", "contacts", "contacts", "--book", "https://contacts.icloud.com/123/addressbooks/victim/", "<D:collection/>", "text/vcard", false},
		{"opaque event href", "calendar", "events", "--calendar", "https://p01-caldav.icloud.com/123/calendars/other/opaque", "", "text/calendar", true},
		{"opaque contact href", "contacts", "contacts", "--book", "https://contacts.icloud.com/123/addressbooks/other/opaque", "", "text/vcard", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, deletes := 0, 0
			http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				response := &http.Response{StatusCode: 204, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}
				if r.URL.String() != tc.target {
					t.Fatalf("changed resource href to %s", r.URL)
				}
				switch r.Method {
				case "PROPFIND":
					reads++
					if r.Header.Get("Depth") != "0" {
						t.Fatal("preflight not Depth0")
					}
					response.StatusCode = 207
					response.Body = io.NopCloser(strings.NewReader(`<D:multistatus xmlns:D="DAV:"><D:response><D:href>` + tc.target + `</D:href><D:propstat><D:prop><D:resourcetype>` + tc.resourceType + `</D:resourcetype><D:getcontenttype>` + tc.contentType + `</D:getcontenttype><D:getetag>"version"</D:getetag></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`))
				case "DELETE":
					deletes++
					if !tc.valid || r.Header.Get("If-Match") != `"version"` {
						t.Fatal("unsafe DELETE emitted")
					}
				default:
					t.Fatalf("unexpected request %s", r.Method)
				}
				return response, nil
			})
			var stdout bytes.Buffer
			code := Run([]string{tc.service, tc.group, "delete", tc.parentFlag, "/selected-collection/", "--id", tc.target}, strings.NewReader(""), &stdout, &bytes.Buffer{})
			wantCode, wantDeletes := output.ExitValidation, 0
			if tc.valid {
				wantCode, wantDeletes = output.ExitOK, 1
			}
			if code != wantCode || deletes != wantDeletes || reads != 1 {
				t.Fatalf("code=%d DELETE=%d reads=%d output=%s", code, deletes, reads, &stdout)
			}
		})
	}
}
