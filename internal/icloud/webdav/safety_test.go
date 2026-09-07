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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCredentialBoundaryAndRedirectDiscovery(t *testing.T) {
	for _, target := range []string{"http://caldav.icloud.com/object.ics", "http://p01-caldav.icloud.com/object.ics", "https://evil.example/object.ics"} {
		t.Run(target, func(t *testing.T) {
			client := New(CalendarBase, config.Config{AppleID: "user", AppPassword: "secret"})
			requests := 0
			client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: 307, Header: http.Header{"Location": []string{target}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})
			if err := client.DeleteEvent(context.Background(), "/calendar/", target); err == nil || requests != 0 {
				t.Fatalf("direct unsafe request: err=%v requests=%d", err, requests)
			}
			if err := client.DeleteEvent(context.Background(), "/calendar/", "object.ics"); err == nil || requests != 1 {
				t.Fatalf("redirect escaped boundary: err=%v requests=%d", err, requests)
			}
		})
	}
	client := New(CalendarBase, config.Config{AppleID: "user", AppPassword: "secret"})
	var paths []string
	client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Host+r.URL.Path)
		user, pass, ok := r.BasicAuth()
		if !ok || user != "user" || pass != "secret" {
			t.Fatal("authorized shard lost authentication")
		}
		if r.Method != "PROPFIND" {
			t.Fatalf("redirect changed discovery method to %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 || r.Header.Get("Depth") == "" {
			t.Fatal("redirect lost discovery body or depth")
		}
		response := &http.Response{StatusCode: 207, Header: http.Header{}, Request: r}
		switch r.URL.Host + r.URL.Path {
		case "caldav.icloud.com/.well-known/caldav":
			response.StatusCode = 301
			response.Header.Set("Location", "https://p01-caldav.icloud.com/discovery/")
			response.Body = io.NopCloser(strings.NewReader(""))
		case "p01-caldav.icloud.com/discovery/":
			response.Body = io.NopCloser(strings.NewReader(`<multistatus xmlns="DAV:"><response><href>/discovery/</href><propstat><prop><current-user-principal><href>/principal/</href></current-user-principal></prop></propstat></response></multistatus>`))
		case "p01-caldav.icloud.com/principal/":
			response.Body = io.NopCloser(strings.NewReader(`<multistatus xmlns="DAV:"><response><href>/principal/</href><propstat><prop><calendar-home-set><href>/calendars/</href></calendar-home-set></prop></propstat></response></multistatus>`))
		case "p01-caldav.icloud.com/calendars/":
			response.Body = io.NopCloser(strings.NewReader(`<multistatus xmlns="DAV:"><response><href>work/</href><propstat><prop><displayname>Work</displayname></prop></propstat></response></multistatus>`))
		default:
			t.Fatalf("wrong discovery destination: %s", r.URL)
		}
		return response, nil
	})
	resources, err := client.ListCalendars(context.Background())
	if err != nil || len(resources) != 1 || resources[0].Href != "https://p01-caldav.icloud.com/calendars/work/" || len(paths) != 4 {
		t.Fatalf("discovery resources=%#v err=%v paths=%v", resources, err, paths)
	}
}

func TestWritesEnforceCreateAndUpdatePreconditions(t *testing.T) {
	for _, kind := range []string{"event", "contact"} {
		t.Run(kind, func(t *testing.T) {
			var stored string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "PUT" {
					t.Fatalf("unexpected method %s", r.Method)
				}
				switch {
				case r.Header.Get("If-None-Match") == "*":
					if stored != "" {
						w.WriteHeader(412)
						return
					}
				case r.Header.Get("If-Match") == "*":
					if stored == "" {
						w.WriteHeader(412)
						return
					}
				case r.Header.Get("If-Match") == `"current"`:
					if stored == "" {
						w.WriteHeader(412)
						return
					}
				default:
					w.WriteHeader(412)
					return
				}
				b, _ := io.ReadAll(r.Body)
				stored = string(b)
				w.WriteHeader(204)
			}))
			defer server.Close()
			client := New(server.URL, config.Config{})
			client.HTTP = server.Client()
			create := client.CreateEvent
			update := client.PutEvent
			if kind == "contact" {
				create = client.CreateContact
				update = client.PutContact
			}
			ctx := context.Background()
			if _, err := update(ctx, "/records/", "same", "missing", ""); err == nil {
				t.Fatal("update created a missing record")
			}
			if _, err := create(ctx, "/records/", "same", "first"); err != nil {
				t.Fatal(err)
			}
			if _, err := create(ctx, "/records/", "same", "second"); err == nil || stored != "first" {
				t.Fatal("create overwrote record")
			}
			if _, err := update(ctx, "/records/", "same", "stale", `"old"`); err == nil || stored != "first" {
				t.Fatal("stale update overwrote record")
			}
			if _, err := update(ctx, "/records/", "same", "updated", `"current"`); err != nil || stored != "updated" {
				t.Fatalf("matching update: err=%v stored=%s", err, stored)
			}
		})
	}
}

func TestCalendarRangeValidationAndOpenBounds(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), `start=""`) || strings.Contains(string(b), `end=""`) {
			t.Errorf("empty range attribute: %s", b)
		}
		w.Write([]byte(`<multistatus xmlns="DAV:"/>`))
	}))
	defer server.Close()
	client := New(server.URL, config.Config{})
	client.HTTP = server.Client()
	for _, bounds := range [][2]string{{"2026-06-09T10:00:00-07:00", ""}, {"", "20260609T170000Z"}} {
		if _, err := client.ListEvents(context.Background(), "/calendar/", bounds[0], bounds[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, bounds := range [][2]string{{"yesterday", ""}, {"2026-02-30T10:00:00Z", ""}, {"20260609T170000Z", "20260609T170000Z"}, {"20260610T170000Z", "20260609T170000Z"}} {
		if _, err := client.ListEvents(context.Background(), "/calendar/", bounds[0], bounds[1]); err == nil {
			t.Fatalf("accepted invalid range %v", bounds)
		}
	}
	if requests != 2 {
		t.Fatalf("invalid range issued requests: %d", requests)
	}
	for _, id := range []string{"", "https://caldav.icloud.com/%zz"} {
		if err := client.DeleteEvent(context.Background(), "/calendar/", id); err == nil {
			t.Fatalf("accepted invalid id %q", id)
		}
	}
	if requests != 2 {
		t.Fatal("invalid id touched a resource")
	}
}
