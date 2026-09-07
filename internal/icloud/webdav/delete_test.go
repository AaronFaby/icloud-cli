package webdav

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aaronfaby/icloud-cli/internal/config"
)

func deleteMetadata(href, resourceType, contentType, etag string) string {
	return `<D:multistatus xmlns:D="DAV:"><D:response><D:href>` + escapeXMLAttr(href) + `</D:href><D:propstat><D:prop><D:resourcetype>` + resourceType + `</D:resourcetype><D:getcontenttype>` + escapeXMLAttr(contentType) + `</D:getcontenttype><D:getetag>` + escapeXMLAttr(etag) + `</D:getetag></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`
}

func deleteResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestDeleteVerifiedOpaqueResource(t *testing.T) {
	for _, tc := range []struct {
		base, contentType string
		contact, redirect bool
	}{
		{CalendarBase, "text/calendar; charset=utf-8", false, false},
		{ContactsBase, "text/vcard; version=3.0", true, false},
		{ContactsBase, "text/x-vcard", true, false},
		{CalendarBase, "text/calendar", false, true},
	} {
		t.Run(tc.contentType+"/"+tc.base, func(t *testing.T) {
			client := New(tc.base, config.Config{AppleID: "dummy", AppPassword: "secret"})
			target := tc.base + "unrelated-collection/opaque-resource?revision=1"
			verifiedTarget := target
			if tc.redirect {
				verifiedTarget = "https://p01-caldav.icloud.com/shard/opaque"
			}
			preflights, deletes := 0, 0
			client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if user, pass, ok := r.BasicAuth(); !ok || user != "dummy" || pass != "secret" {
					t.Fatal("lost auth")
				}
				if r.Method == "PROPFIND" {
					preflights++
					if r.Header.Get("Depth") != "0" {
						t.Fatal("metadata request must have Depth0")
					}
					if tc.redirect && r.URL.String() == target {
						resp := deleteResponse(r, 301, "")
						resp.Header.Set("Location", verifiedTarget)
						return resp, nil
					}
					return deleteResponse(r, 207, deleteMetadata(r.URL.String(), "", tc.contentType, `"version"`)), nil
				}
				if r.Method != "DELETE" || r.URL.String() != verifiedTarget || r.Header.Get("If-Match") != `"version"` {
					t.Fatalf("unsafe mutation: %s %s %v", r.Method, r.URL, r.Header)
				}
				deletes++
				return deleteResponse(r, 204, ""), nil
			})
			remove := client.DeleteEvent
			if tc.contact {
				remove = client.DeleteContact
			}
			if err := remove(context.Background(), "/selected-collection/", target); err != nil {
				t.Fatal(err)
			}
			if deletes != 1 || preflights < 1 {
				t.Fatalf("DELETE=%d PROPFIND=%d", deletes, preflights)
			}
		})
	}
}

func TestDeleteRejectsUnverifiedMetadata(t *testing.T) {
	target := "https://caldav.icloud.com/collection/opaque"
	valid := deleteMetadata(target, "", "text/calendar", `"version"`)
	cases := map[string]string{
		"collection":              deleteMetadata(target, `<D:collection/>`, "text/calendar", `"version"`),
		"foreign type":            deleteMetadata(target, `<X:collection xmlns:X="urn:evil"/>`, "text/calendar", `"version"`),
		"type text":               deleteMetadata(target, "collection", "text/calendar", `"version"`),
		"contact":                 deleteMetadata(target, "", "text/vcard", `"version"`),
		"missing type":            strings.Replace(valid, "<D:resourcetype></D:resourcetype>", "", 1),
		"foreign metadata":        strings.ReplaceAll(valid, `xmlns:D="DAV:"`, `xmlns:D="urn:evil"`),
		"foreign property":        strings.Replace(valid, "<D:resourcetype></D:resourcetype>", `<X:resourcetype xmlns:X="urn:evil"/>`, 1),
		"missing content type":    deleteMetadata(target, "", "", `"version"`),
		"missing ETag":            deleteMetadata(target, "", "text/calendar", ""),
		"weak ETag":               deleteMetadata(target, "", "text/calendar", `W/"version"`),
		"wildcard ETag":           deleteMetadata(target, "", "text/calendar", `*`),
		"ETag list":               deleteMetadata(target, "", "text/calendar", `"one", "two"`),
		"failed properties":       strings.Replace(valid, "200 OK", "404 Not Found", 1),
		"missing property status": strings.Replace(valid, "<D:status>HTTP/1.1 200 OK</D:status>", "", 1),
		"invalid property status": strings.Replace(valid, "HTTP/1.1 200 OK", "fake 200 OK", 1),
		"wrong href":              deleteMetadata("/collection/another", "", "text/calendar", `"version"`),
		"duplicate href":          strings.Replace(valid, "</D:href>", "</D:href><D:href>/collection/another</D:href>", 1),
		"duplicate type":          strings.Replace(valid, "</D:resourcetype>", "</D:resourcetype><D:resourcetype/>", 1),
		"duplicate status":        strings.Replace(valid, "</D:status>", "</D:status><D:status>HTTP/1.1 404 Not Found</D:status>", 1),
		"duplicate response":      strings.Replace(valid, "</D:response>", "</D:response><D:response/>", 1),
		"duplicate ETag":          strings.Replace(valid, "</D:getetag>", "</D:getetag><D:getetag>other</D:getetag>", 1),
		"extra document":          valid + valid,
		"oversized metadata":      valid + strings.Repeat(" ", 64<<10),
	}
	for name, metadata := range cases {
		t.Run(name, func(t *testing.T) {
			client := New(CalendarBase, config.Config{})
			deletes := 0
			client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == "DELETE" {
					deletes++
					return deleteResponse(r, 204, ""), nil
				}
				return deleteResponse(r, 207, metadata), nil
			})
			if err := client.DeleteEvent(context.Background(), "/collection/", target); err == nil || deletes != 0 {
				t.Fatalf("unsafe metadata accepted: err=%v DELETE=%d", err, deletes)
			}
		})
	}
	for _, target := range []string{CalendarBase, strings.TrimSuffix(CalendarBase, "/"), CalendarBase + "#fragment"} {
		client := New(CalendarBase, config.Config{})
		client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			t.Fatal("root target should be refused before a request")
			return nil, nil
		})
		if err := client.DeleteEvent(context.Background(), "/collection/", target); err == nil {
			t.Fatal("root accepted")
		}
	}
}

func TestDeleteRefusesRedirectsRacesAndIncompleteResults(t *testing.T) {
	for _, status := range []int{200, 204, 202, 207, 301, 302, 307, 308, 412} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := New(CalendarBase, config.Config{})
			deletes := 0
			client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == "PROPFIND" {
					return deleteResponse(r, 207, deleteMetadata(r.URL.String(), "", "text/calendar", `"before-race"`)), nil
				}
				deletes++
				if deletes > 1 || r.Header.Get("If-Match") != `"before-race"` {
					t.Fatal("DELETE followed redirect or lost precondition")
				}
				response := deleteResponse(r, status, "")
				response.Header.Set("Location", "https://contacts.icloud.com/another-collection/")
				return response, nil
			})
			err := client.DeleteEvent(context.Background(), "/collection/", "opaque")
			if (err == nil) != (status == 200 || status == 204) || deletes != 1 {
				t.Fatalf("status=%d err=%v DELETE=%d", status, err, deletes)
			}
		})
	}
}
