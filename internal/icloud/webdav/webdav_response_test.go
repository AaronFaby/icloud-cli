package webdav

import "testing"

func TestMultistatusRejectsUnrelatedXML(t *testing.T) {
	for _, body := range []string{"<html><body>Access denied</body></html>", "<multistatus/>"} {
		if _, err := parseMultistatus([]byte(body)); err == nil {
			t.Fatalf("accepted unrelated XML: %s", body)
		}
	}
}

func TestMultistatusIgnoresFailedPropertiesAndResources(t *testing.T) {
	body := `<D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
<D:response><D:href>/calendar/</D:href>
<D:propstat><D:prop><D:displayname>Work</D:displayname><D:getetag>good</D:getetag></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
<D:propstat><D:prop><D:displayname>Failed</D:displayname><D:getetag>bad</D:getetag><C:calendar-home-set><D:href>/bad/</D:href></C:calendar-home-set></D:prop><D:status>HTTP/1.1 404 Not Found</D:status></D:propstat>
</D:response>
<D:response><D:href>/gone.ics</D:href><D:status>HTTP/1.1 404 Not Found</D:status></D:response>
</D:multistatus>`
	resources, err := parseMultistatus([]byte(body))
	if err != nil || len(resources) != 1 {
		t.Fatalf("resources=%+v err=%v", resources, err)
	}
	if resources[0].DisplayName != "Work" || resources[0].ETag != "good" || firstPropHref(resources, "calendar-home-set") != "" {
		t.Fatalf("failed properties merged into success: %+v", resources[0])
	}
}
