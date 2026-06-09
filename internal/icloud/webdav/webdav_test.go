package webdav

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaronfaby/icloud-cli/internal/config"
)

func TestListCalendarsDiscoversCalendarHomeSet(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Path {
		case "/.well-known/caldav":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/.well-known/caldav</D:href>
    <D:propstat>
      <D:prop>
        <D:current-user-principal><D:href>/principal/</D:href></D:current-user-principal>
      </D:prop>
    </D:propstat>
  </D:response>
</D:multistatus>`))
		case "/principal/":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:response>
    <D:href>/principal/</D:href>
    <D:propstat>
      <D:prop>
        <C:calendar-home-set><D:href>/calendars/</D:href></C:calendar-home-set>
      </D:prop>
    </D:propstat>
  </D:response>
</D:multistatus>`))
		case "/calendars/":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/calendars/work/</D:href>
    <D:propstat>
      <D:prop>
        <D:displayname>Work</D:displayname>
        <D:resourcetype><D:collection/></D:resourcetype>
      </D:prop>
    </D:propstat>
  </D:response>
</D:multistatus>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	resources, err := client.ListCalendars(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].Href != "/calendars/work/" {
		t.Fatalf("unexpected resources: %#v", resources)
	}
	want := []string{"/.well-known/caldav", "/principal/", "/calendars/"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %#v, want %#v", paths, want)
		}
	}
}

func TestListAddressBooksFallsBackToBaseWhenPrincipalMissing(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Path {
		case "/.well-known/carddav":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/.well-known/carddav</D:href>
    <D:propstat><D:prop/></D:propstat>
  </D:response>
</D:multistatus>`))
		case "/":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/card/</D:href>
    <D:propstat>
      <D:prop>
        <D:displayname>Contacts</D:displayname>
        <D:resourcetype><D:collection/></D:resourcetype>
      </D:prop>
    </D:propstat>
  </D:response>
</D:multistatus>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	resources, err := client.ListAddressBooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].Href != "/card/" {
		t.Fatalf("unexpected resources: %#v", resources)
	}
	want := []string{"/.well-known/carddav", "/"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %#v, want %#v", paths, want)
		}
	}
}

func TestListAddressBooksDiscoversAddressBookHomeSet(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Path {
		case "/.well-known/carddav":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/.well-known/carddav</D:href>
    <D:propstat>
      <D:prop>
        <D:principal-URL><D:href>/principal/</D:href></D:principal-URL>
      </D:prop>
    </D:propstat>
  </D:response>
</D:multistatus>`))
		case "/principal/":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
  <D:response>
    <D:href>/principal/</D:href>
    <D:propstat>
      <D:prop>
        <C:addressbook-home-set><D:href>/addressbooks/</D:href></C:addressbook-home-set>
      </D:prop>
    </D:propstat>
  </D:response>
</D:multistatus>`))
		case "/addressbooks/":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
  <D:response>
    <D:href>/addressbooks/personal/</D:href>
    <D:propstat>
      <D:prop>
        <D:displayname>Personal</D:displayname>
        <D:resourcetype><D:collection/><C:addressbook/></D:resourcetype>
      </D:prop>
    </D:propstat>
  </D:response>
</D:multistatus>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	resources, err := client.ListAddressBooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].Href != "/addressbooks/personal/" || resources[0].DisplayName != "Personal" {
		t.Fatalf("unexpected resources: %#v", resources)
	}
	want := []string{"/.well-known/carddav", "/principal/", "/addressbooks/"}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}

func TestListEventsSendsCalendarQueryReport(t *testing.T) {
	var gotMethod, gotDepth, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotDepth = r.Header.Get("Depth")
		username, password, _ := r.BasicAuth()
		gotAuth = username + ":" + password
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		if r.URL.Path != "/calendars/work/" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:response>
    <D:href>/calendars/work/event-1.ics</D:href>
    <D:propstat>
      <D:prop>
        <D:getetag>"abc"</D:getetag>
        <C:calendar-data>BEGIN:VCALENDAR&#x0A;BEGIN:VEVENT&#x0A;UID:event-1&#x0A;SUMMARY:Planning&#x0A;END:VEVENT&#x0A;END:VCALENDAR</C:calendar-data>
      </D:prop>
    </D:propstat>
  </D:response>
</D:multistatus>`))
	}))
	defer server.Close()

	client := New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	resources, err := client.ListEvents(context.Background(), "/calendars/work/", "2026-06-09T10:00:00-07:00", "2026-06-09T11:30:00-07:00")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "REPORT" || gotDepth != "1" || gotAuth != "user:pass" {
		t.Fatalf("request method/depth/auth = %q/%q/%q", gotMethod, gotDepth, gotAuth)
	}
	if !strings.Contains(gotBody, `<C:calendar-query`) || !strings.Contains(gotBody, `<C:time-range start="20260609T170000Z" end="20260609T183000Z"/>`) {
		t.Fatalf("unexpected report body: %s", gotBody)
	}
	if len(resources) != 1 || resources[0].Href != "/calendars/work/event-1.ics" || resources[0].ETag != `"abc"` {
		t.Fatalf("unexpected resources: %#v", resources)
	}
	if !strings.Contains(resources[0].Data, "SUMMARY:Planning") {
		t.Fatalf("calendar data = %q", resources[0].Data)
	}
}

func TestPutEventUsesHrefIDAndCalendarContentType(t *testing.T) {
	var gotMethod, gotPath, gotType, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("ETag", `"updated"`)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	data := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:event-1\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	resource, err := client.PutEvent(context.Background(), "/calendars/work/", "/calendars/work/event-1.ics", data)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "PUT" || gotPath != "/calendars/work/event-1.ics" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if gotType != "text/calendar; charset=utf-8" {
		t.Fatalf("content type = %q", gotType)
	}
	if gotBody != data {
		t.Fatalf("body = %q", gotBody)
	}
	if resource.ETag != `"updated"` || resource.Data != data {
		t.Fatalf("resource = %#v", resource)
	}
}

func TestPutEventDerivesIDFromUID(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	_, err := client.PutEvent(context.Background(), "/calendars/work/", "", "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:planning-1\nEND:VEVENT\nEND:VCALENDAR\n")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/calendars/work/planning-1.ics" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestDeleteEventAcceptsHrefAndResourceID(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Fatalf("method = %q", r.Method)
		}
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	if err := client.DeleteEvent(context.Background(), "/calendars/work/", "event-1"); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteEvent(context.Background(), "/calendars/work/", "/calendars/work/event-2.ics"); err != nil {
		t.Fatal(err)
	}

	want := []string{"/calendars/work/event-1.ics", "/calendars/work/event-2.ics"}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}

func TestCalendarQueryBodyEscapesFallbackTimeRange(t *testing.T) {
	body := calendarQueryBody(`20260609T170000Z" bad="x`, `20260609T180000Z&bad`)
	if !strings.Contains(body, `start="20260609T170000Z&#34; bad=&#34;x"`) {
		t.Fatalf("start was not escaped: %s", body)
	}
	if !strings.Contains(body, `end="20260609T180000Z&amp;bad"`) {
		t.Fatalf("end was not escaped: %s", body)
	}
}

func TestParseMultistatusReadsResourceTypesAndHomeSets(t *testing.T) {
	resources, err := parseMultistatus([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:response>
    <D:href>/principal/</D:href>
    <D:propstat>
      <D:prop>
        <D:displayname>Principal</D:displayname>
        <D:resourcetype><D:collection/><C:calendar/></D:resourcetype>
        <C:calendar-home-set><D:href>/calendars/</D:href></C:calendar-home-set>
      </D:prop>
    </D:propstat>
  </D:response>
</D:multistatus>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 {
		t.Fatalf("resources = %#v", resources)
	}
	got := resources[0]
	if got.DisplayName != "Principal" || strings.Join(got.ResourceTypes, ",") != "collection,calendar" {
		t.Fatalf("resource = %#v", got)
	}
	if firstPropHref(resources, "calendar-home-set") != "/calendars/" {
		t.Fatalf("home set = %q", firstPropHref(resources, "calendar-home-set"))
	}
}

func TestListContactsSendsAddressBookQueryReport(t *testing.T) {
	var gotMethod, gotDepth, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotDepth = r.Header.Get("Depth")
		username, password, _ := r.BasicAuth()
		gotAuth = username + ":" + password
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		if r.URL.Path != "/addressbooks/personal/" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
  <D:response>
    <D:href>/addressbooks/personal/contact-1.vcf</D:href>
    <D:propstat>
      <D:prop>
        <D:getetag>"contact-etag"</D:getetag>
        <C:address-data>BEGIN:VCARD&#x0A;VERSION:3.0&#x0A;UID:contact-1&#x0A;FN:Ada Lovelace&#x0A;END:VCARD</C:address-data>
      </D:prop>
    </D:propstat>
  </D:response>
</D:multistatus>`))
	}))
	defer server.Close()

	client := New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	resources, err := client.ListContacts(context.Background(), "/addressbooks/personal/")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "REPORT" || gotDepth != "1" || gotAuth != "user:pass" {
		t.Fatalf("request method/depth/auth = %q/%q/%q", gotMethod, gotDepth, gotAuth)
	}
	if !strings.Contains(gotBody, `<C:addressbook-query`) || !strings.Contains(gotBody, `<C:address-data/>`) {
		t.Fatalf("unexpected report body: %s", gotBody)
	}
	if len(resources) != 1 || resources[0].Href != "/addressbooks/personal/contact-1.vcf" || resources[0].ETag != `"contact-etag"` {
		t.Fatalf("unexpected resources: %#v", resources)
	}
	if !strings.Contains(resources[0].Data, "FN:Ada Lovelace") {
		t.Fatalf("address data = %q", resources[0].Data)
	}
}

func TestGetContactUsesHrefAndReturnsVCard(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("ETag", `"get-etag"`)
		_, _ = w.Write([]byte("BEGIN:VCARD\r\nUID:contact-1\r\nFN:Ada Lovelace\r\nEND:VCARD\r\n"))
	}))
	defer server.Close()

	client := New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	resource, err := client.GetContact(context.Background(), "/addressbooks/personal/", "/addressbooks/personal/contact-1.vcf")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "GET" || gotPath != "/addressbooks/personal/contact-1.vcf" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if resource.ETag != `"get-etag"` || !strings.Contains(resource.Data, "FN:Ada Lovelace") {
		t.Fatalf("resource = %#v", resource)
	}
}

func TestPutContactUsesHrefIDAndVCardContentType(t *testing.T) {
	var gotMethod, gotPath, gotType, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("ETag", `"updated-contact"`)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	data := "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:contact-1\r\nFN:Ada Lovelace\r\nEND:VCARD\r\n"
	resource, err := client.PutContact(context.Background(), "/addressbooks/personal/", "/addressbooks/personal/contact-1.vcf", data)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "PUT" || gotPath != "/addressbooks/personal/contact-1.vcf" {
		t.Fatalf("request = %s %s", gotMethod, gotPath)
	}
	if gotType != "text/vcard; charset=utf-8" {
		t.Fatalf("content type = %q", gotType)
	}
	if gotBody != data {
		t.Fatalf("body = %q", gotBody)
	}
	if resource.ETag != `"updated-contact"` || resource.Data != data {
		t.Fatalf("resource = %#v", resource)
	}
}

func TestPutContactDerivesIDFromUID(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	_, err := client.PutContact(context.Background(), "/addressbooks/personal/", "", "BEGIN:VCARD\nVERSION:3.0\nUID:ada-1\nFN:Ada Lovelace\nEND:VCARD\n")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/addressbooks/personal/ada-1.vcf" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestDeleteContactAcceptsHrefAndResourceID(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Fatalf("method = %q", r.Method)
		}
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := New(server.URL+"/", config.Config{AppleID: "user", AppPassword: "pass"})
	if err := client.DeleteContact(context.Background(), "/addressbooks/personal/", "contact-1"); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteContact(context.Background(), "/addressbooks/personal/", "/addressbooks/personal/contact-2.vcf"); err != nil {
		t.Fatal(err)
	}

	want := []string{"/addressbooks/personal/contact-1.vcf", "/addressbooks/personal/contact-2.vcf"}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}
