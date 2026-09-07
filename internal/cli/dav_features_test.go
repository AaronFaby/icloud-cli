package cli

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDAVFeaturesHelpAndPositionalValidation(t *testing.T) {
	t.Setenv("ICLOUD_CLI_LOG", "off")
	for _, command := range [][]string{{"calendar", "events", "patch"}, {"contacts", "contacts", "patch"}, {"contacts", "contacts", "search"}} {
		var out bytes.Buffer
		args := append(append([]string{}, command...), "--help")
		if code := Run(args, strings.NewReader(""), &out, &out); code != 0 || !strings.Contains(out.String(), `"ok": true`) {
			t.Fatalf("%v %s", args, out.String())
		}
		out.Reset()
		args = append(append([]string{}, command...), "unexpected")
		if code := Run(args, strings.NewReader(""), &out, &out); code == 0 {
			t.Fatal("accepted positional")
		}
	}
}
func TestBuildCalendarAllDayAndTimezone(t *testing.T) {
	data, err := buildCalendarData(eventInput{Summary: "Day", Start: "2026-09-07", End: "2026-09-08", AllDay: true})
	if err != nil || !strings.Contains(data, "DTSTART;VALUE=DATE:20260907\r\nDTEND;VALUE=DATE:20260908") {
		t.Fatalf("%s %v", data, err)
	}
	data, err = buildCalendarData(eventInput{Summary: "Meeting", Start: "2026-09-07T09:00", End: "2026-09-07T10:00", TimeZone: "America/Los_Angeles"})
	if err != nil || !strings.Contains(data, "DTSTART:20260907T160000Z") {
		t.Fatalf("%s %v", data, err)
	}
}

func TestDAVCLIPatchAndSearchRoundtrip(t *testing.T) {
	t.Setenv("ICLOUD_CLI_LOG", "off")
	t.Setenv("ICLOUD_APPLE_ID", "dummy")
	t.Setenv("ICLOUD_APP_PASSWORD", "secret")
	t.Setenv("ICLOUD_CONFIG", t.TempDir()+"/missing")
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	puts, reports := 0, 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}
		body := ""
		switch r.Method {
		case "PROPFIND":
			response.StatusCode = 207
			body = `<D:multistatus xmlns:D="DAV:"><D:response><D:href>` + r.URL.String() + `</D:href><D:propstat><D:prop><D:resourcetype/><D:getcontenttype>text/vcard</D:getcontenttype><D:getetag>"v1"</D:getetag></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`
		case "GET":
			response.Header.Set("ETag", `"v1"`)
			body = "BEGIN:VCARD\r\nUID:stable\r\nFN:Name\r\nNOTE:old\r\nX-CUSTOM:keep\r\nEND:VCARD\r\n"
		case "PUT":
			puts++
			data, _ := io.ReadAll(r.Body)
			if r.Header.Get("If-Match") != `"v1"` || strings.Contains(string(data), "NOTE:") || !strings.Contains(string(data), "X-CUSTOM:keep") {
				t.Fatalf("unsafe patch %s %v", data, r.Header)
			}
			response.StatusCode = 204
		case "REPORT":
			reports++
			data, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(data), "A&amp;B") || !strings.Contains(string(data), `name="EMAIL"`) {
				t.Fatalf("filter %s", data)
			}
			response.StatusCode = 207
			body = `<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav"><D:response><D:href>/book/a.vcf</D:href><D:propstat><D:prop><C:address-data>BEGIN:VCARD&#10;UID:stable&#10;FN:Found Person&#10;EMAIL:a@example.com&#10;END:VCARD&#10;</C:address-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`
		default:
			t.Fatal(r.Method)
		}
		response.Body = io.NopCloser(strings.NewReader(body))
		return response, nil
	})
	var out bytes.Buffer
	if code := Run([]string{"contacts", "contacts", "patch", "--book", "/book/", "--id", "a", "--input-json", `{"note":null}`}, strings.NewReader(""), &out, &bytes.Buffer{}); code != 0 || puts != 1 {
		t.Fatalf("patch %d %s", code, &out)
	}
	out.Reset()
	if code := Run([]string{"contacts", "contacts", "search", "--book", "/book/", "--email", "A&B", "--limit", "2"}, strings.NewReader(""), &out, &bytes.Buffer{}); code != 0 || reports != 1 || !strings.Contains(out.String(), `"formatted_name": "Found Person"`) {
		t.Fatalf("search %d %s", code, &out)
	}
}

func TestContactSearchCLIRejectsInvalidLimitsBeforeNetwork(t *testing.T) {
	t.Setenv("ICLOUD_CLI_LOG", "off")
	t.Setenv("ICLOUD_APPLE_ID", "")
	t.Setenv("ICLOUD_APP_PASSWORD", "")
	t.Setenv("ICLOUD_CONFIG", t.TempDir()+"/missing")
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	requests := 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		t.Fatal("unexpected network")
		return nil, nil
	})
	for _, limit := range []string{"0", "-1", "1001"} {
		var out bytes.Buffer
		code := Run([]string{"contacts", "contacts", "search", "--book", "/book/", "--query", "name", "--limit", limit}, strings.NewReader(""), &out, &bytes.Buffer{})
		if code != 2 || requests != 0 || !strings.Contains(out.String(), "invalid_search_limit") {
			t.Fatalf("limit%s code%d requests%d %s", limit, code, requests, &out)
		}
	}
}
