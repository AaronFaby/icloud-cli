package webdav

import (
	"context"
	"net/http"
	"net/http/httptest"
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
