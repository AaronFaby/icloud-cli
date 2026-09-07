package webdav

import (
	"context"
	"encoding/json"
	"github.com/aaronfaby/icloud-cli/internal/config"
	"io"
	"net/http"
	"strings"
	"testing"
)

func patchJSON(s string) map[string]json.RawMessage {
	var p map[string]json.RawMessage
	_ = json.Unmarshal([]byte(s), &p)
	return p
}
func TestPatchPreservesUntargetedComponents(t *testing.T) {
	data := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:stable\r\nSUMMARY;LANGUAGE=en:Old\r\nDESCRIPTION:Long\r\n folded\r\nRRULE:FREQ=WEEKLY\r\nATTENDEE;CN=Person:mailto:a@example.com\r\nX-CUSTOM:keep\r\nBEGIN:VALARM\r\nSUMMARY:Alarm\r\nACTION:DISPLAY\r\nEND:VALARM\r\nEND:VEVENT\r\nBEGIN:VEVENT\r\nUID:stable\r\nRECURRENCE-ID:20260907T120000Z\r\nSUMMARY:Exception\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	got, err := patchContent(data, "VEVENT", patchJSON(`{"summary":"New","location":"Room, One"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(data, "SUMMARY;LANGUAGE=en:Old", "SUMMARY:New", 1)
	want = strings.Replace(want, "END:VEVENT", "LOCATION:Room\\, One\r\nEND:VEVENT", 1)
	if got != want {
		t.Fatalf("untargeted content changed:\n%s", got)
	}
	card := "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:stable\r\nFN:A Person\r\nN;LANGUAGE=en:Family;Given,Other;Middle;Dr.;Jr.\r\nitem1.EMAIL;TYPE=HOME:a@example.com\r\nEMAIL;TYPE=WORK:b@example.com\r\nX-OTHER:retained\r\nEND:VCARD\r\n"
	got, err = patchContent(card, "VCARD", patchJSON(`{"family_name":"New;Family","emails":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `N;LANGUAGE=en:New\;Family;Given,Other;Middle;Dr.;Jr.`) || strings.Contains(got, "EMAIL") || !strings.Contains(got, "X-OTHER:retained") {
		t.Fatal(got)
	}
}
func TestPatchConditionalWriteAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name, etag, patch string
		status            int
		wantPut           bool
	}{
		{"success", `"v1"`, `{"note":null}`, 204, true}, {"concurrent GET", `"v2"`, `{"note":null}`, 204, false}, {"weak GET", `W/"v1"`, `{"note":null}`, 204, false}, {"unknown", `"v1"`, `{"surprise":"x"}`, 204, false}, {"null required", `"v1"`, `{"formatted_name":null}`, 204, false}, {"concurrent PUT", `"v1"`, `{"note":"x"}`, 412, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(ContactsBase, config.Config{})
			puts := 0
			c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				switch r.Method {
				case "PROPFIND":
					return deleteResponse(r, 207, deleteMetadata(r.URL.String(), "", "text/vcard", `"v1"`)), nil
				case "GET":
					resp := deleteResponse(r, 200, "BEGIN:VCARD\r\nUID:stable\r\nFN:Name\r\nNOTE:old\r\nEND:VCARD\r\n")
					resp.Header.Set("ETag", tc.etag)
					return resp, nil
				case "PUT":
					puts++
					if r.Header.Get("If-Match") != `"v1"` {
						t.Fatal("unconditional write")
					}
					return deleteResponse(r, tc.status, ""), nil
				}
				t.Fatal(r.Method)
				return nil, nil
			})
			_, err := c.PatchContact(context.Background(), "/book/", "item", patchJSON(tc.patch))
			if (puts == 1) != tc.wantPut {
				t.Fatalf("puts=%d err=%v", puts, err)
			}
			if (tc.name == "success") != (err == nil) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
func TestEventTimesDSTAndDates(t *testing.T) {
	for _, tc := range []struct {
		start, end, zone string
		all, ok          bool
	}{
		{"2026-09-07", "2026-09-08", "", true, true}, {"2026-09-07", "2026-09-07", "", true, false}, {"2026-09-07", "2026-09-08", "America/Los_Angeles", true, false},
		{"2026-03-08T02:30:00", "2026-03-08T04:00:00", "America/Los_Angeles", false, false}, {"2026-11-01T01:30:00", "2026-11-01T03:00:00", "America/Los_Angeles", false, false}, {"2026-11-01T01:30:00-07:00", "2026-11-01T03:00:00-08:00", "America/Los_Angeles", false, true}, {"2026-09-07T09:00", "2026-09-07T10:00", "America/Los_Angeles", false, true},
	} {
		_, err := EventTimes(tc.start, tc.end, tc.all, tc.zone)
		if (err == nil) != tc.ok {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}
func TestSearchContactsServerFilter(t *testing.T) {
	c := New(ContactsBase, config.Config{})
	calls := 0
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if r.Method != "REPORT" || !strings.Contains(string(body), `<C:text-match collation="i;unicode-casemap" match-type="contains">A&amp;B&lt;</C:text-match>`) || !strings.Contains(string(body), `<C:nresults>2</C:nresults>`) {
			t.Fatalf("%s %s", r.Method, body)
		}
		return deleteResponse(r, 207, `<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav"><D:response><D:href>/book/a.vcf</D:href><D:propstat><D:prop><C:address-data>BEGIN:VCARD&#13;&#10;VERSION:3.0&#13;&#10;FN:A&amp;B&#13;&#10;N:Family;Given;;; &#13;&#10;item1.EMAIL;TYPE=HOME:a@example.com&#13;&#10;END:VCARD&#13;&#10;</C:address-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`), nil
	})
	results, err := c.SearchContacts(context.Background(), "/book/", ContactSearch{Query: "A&B<", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(results) != 1 || results[0].Contact.FormattedName != "A&B" || results[0].Contact.Emails[0] != "a@example.com" {
		t.Fatalf("%+v", results)
	}
}

func TestPatchSingleTimePreservesOtherAndRecurringTimeRejected(t *testing.T) {
	data := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:stable\r\nDTSTART;VALUE=DATE:20260907\r\nDTEND;VALUE=DATE:20260910\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	got, err := patchContent(data, "VEVENT", patchJSON(`{"start":"2026-09-08"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Replace(data, "20260907", "20260908", 1) {
		t.Fatal(got)
	}
	recurring := strings.Replace(data, "UID:stable", "UID:stable\r\nRRULE:FREQ=DAILY", 1)
	if _, err := patchContent(recurring, "VEVENT", patchJSON(`{"start":"2026-09-08"}`)); err == nil {
		t.Fatal("recurrence time was rewritten")
	}
}

type repeatedByteReader struct{}

func (repeatedByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}
func TestDAVResponseBound(t *testing.T) {
	if _, err := readDAVBody(repeatedByteReader{}); err == nil {
		t.Fatal("unbounded DAV body")
	}
}

func TestPatchEndRemovesDurationAndFoldedInputBound(t *testing.T) {
	data := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:stable\r\nDTSTART:20260907T100000Z\r\nDURATION:PT1H\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	got, err := patchContent(data, "VEVENT", patchJSON(`{"end":"2026-09-07T12:00:00Z"}`))
	if err != nil || strings.Contains(got, "DURATION") || !strings.Contains(got, "DTEND:20260907T120000Z") {
		t.Fatalf("%s %v", got, err)
	}
	folded := "BEGIN:VCARD\r\nUID:x\r\nPHOTO:x\r\n" + strings.Repeat(" x\r\n", 100001) + "END:VCARD\r\n"
	if _, err := parseContent(folded, "VCARD"); err == nil {
		t.Fatal("physical line bound missing")
	}
}
func TestPatchTimezoneSerializesChangedEndpointUTC(t *testing.T) {
	data := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:stable\r\nDTSTART;TZID=America/Los_Angeles:20260907T090000\r\nDTEND;TZID=America/Los_Angeles:20260907T110000\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	got, err := patchContent(data, "VEVENT", patchJSON(`{"start":"2026-09-07T10:00"}`))
	if err != nil || got != strings.Replace(data, "DTSTART;TZID=America/Los_Angeles:20260907T090000", "DTSTART:20260907T170000Z", 1) {
		t.Fatalf("%s %v", got, err)
	}
}

func TestPatchTimezoneExplicitFoldKeepsInstant(t *testing.T) {
	data := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:stable\r\nDTSTART:20261101T080000Z\r\nDTEND:20261101T110000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	got, err := patchContent(data, "VEVENT", patchJSON(`{"start":"2026-11-01T01:30:00-08:00","end":"2026-11-01T03:00:00-08:00","time_zone":"America/Los_Angeles"}`))
	if err != nil || !strings.Contains(got, "DTSTART:20261101T093000Z") || strings.Contains(got, "TZID") {
		t.Fatalf("%s %v", got, err)
	}
}

func TestEventTimesRejectsLossyPrecisionAndYearOverflow(t *testing.T) {
	for _, times := range [][2]string{{"2026-09-07T12:00:00.1Z", "2026-09-07T12:00:00.9Z"}, {"9999-12-31T22:00:00-02:00", "9999-12-31T23:00:00-02:00"}} {
		if _, err := EventTimes(times[0], times[1], false, ""); err == nil {
			t.Fatalf("lossy times accepted %v", times)
		}
	}
}

func TestPatchDateParameterCaseInsensitive(t *testing.T) {
	data := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:stable\r\nDTSTART;VALUE=date:20260907\r\nDTEND;VALUE=date:20260910\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	got, err := patchContent(data, "VEVENT", patchJSON(`{"start":"2026-09-08"}`))
	if err != nil || !strings.Contains(got, "DTSTART;VALUE=DATE:20260908") || !strings.Contains(got, "DTEND;VALUE=date:20260910") {
		t.Fatalf("%s %v", got, err)
	}
}
