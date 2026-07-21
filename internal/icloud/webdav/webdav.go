package webdav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aaronfaby/icloud-cli/internal/config"
	"github.com/aaronfaby/icloud-cli/internal/logging"
	"github.com/aaronfaby/icloud-cli/internal/output"
)

const (
	CalendarBase = "https://caldav.icloud.com/"
	ContactsBase = "https://contacts.icloud.com/"
)

type Resource struct {
	Href          string   `json:"href"`
	DisplayName   string   `json:"display_name,omitempty"`
	ResourceTypes []string `json:"resource_types,omitempty"`
	ETag          string   `json:"etag,omitempty"`
	Data          string   `json:"data,omitempty"`
	propHrefs     map[string]string
}

type Client struct {
	BaseURL string
	Config  config.Config
	HTTP    *http.Client
}

func New(baseURL string, cfg config.Config) *Client {
	return &Client{
		BaseURL: baseURL,
		Config:  cfg,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) ListCalendars(ctx context.Context) ([]Resource, error) {
	homeURL, err := c.discoverHomeSet(ctx, ".well-known/caldav", currentUserPrincipalBody(), calendarHomeSetBody(), "calendar-home-set")
	if err != nil {
		return nil, wrapRemote("caldav_discovery_failed", "failed to discover iCloud calendar home", err)
	}
	resources, err := c.propfind(ctx, homeURL, calendarBody(), "1")
	if err != nil {
		return nil, wrapRemote("caldav_list_failed", "failed to list iCloud calendars", err)
	}
	logging.Info("caldav_calendars_listed", "count", len(resources))
	return resources, nil
}

func (c *Client) ListAddressBooks(ctx context.Context) ([]Resource, error) {
	homeURL, err := c.discoverHomeSet(ctx, ".well-known/carddav", currentUserPrincipalBody(), addressBookHomeSetBody(), "addressbook-home-set")
	if isExitCode(err, "webdav_discovery_failed") {
		homeURL = c.BaseURL
	} else if err != nil {
		return nil, wrapRemote("carddav_discovery_failed", "failed to discover iCloud address book home", err)
	}
	resources, err := c.propfind(ctx, homeURL, addressBookBody(), "1")
	if err != nil {
		return nil, wrapRemote("carddav_list_failed", "failed to list iCloud address books", err)
	}
	logging.Info("carddav_books_listed", "count", len(resources))
	return resources, nil
}

func (c *Client) ListEvents(ctx context.Context, calendarHref, from, to string) ([]Resource, error) {
	resources, err := c.report(ctx, c.resourceURL(calendarHref), calendarQueryBody(from, to), "1")
	if err != nil {
		return nil, wrapRemote("caldav_events_list_failed", "failed to list iCloud calendar events", err)
	}
	logging.Info("caldav_events_listed", "count", len(resources))
	return resources, nil
}

func (c *Client) PutEvent(ctx context.Context, calendarHref, id, calendarData string) (Resource, error) {
	if strings.TrimSpace(id) == "" {
		id = eventUID(calendarData)
	}
	if !strings.HasSuffix(id, ".ics") {
		id += ".ics"
	}
	requestURL := c.eventURL(calendarHref, id)
	if requestURL == "" {
		return Resource{}, output.Validation("invalid_event_id", "event id is invalid", map[string]string{"id": id})
	}
	return c.put(ctx, requestURL, "text/calendar; charset=utf-8", calendarData)
}

func (c *Client) DeleteEvent(ctx context.Context, calendarHref, id string) error {
	requestURL := c.eventURL(calendarHref, id)
	if requestURL == "" {
		return output.Validation("invalid_event_id", "event id is invalid", map[string]string{"id": id})
	}
	return c.delete(ctx, requestURL)
}

func (c *Client) ListContacts(ctx context.Context, bookHref string) ([]Resource, error) {
	resources, err := c.report(ctx, c.resourceURL(bookHref), addressBookQueryBody(), "1")
	if err != nil {
		return nil, wrapRemote("carddav_contacts_list_failed", "failed to list iCloud contacts", err)
	}
	logging.Info("carddav_contacts_listed", "count", len(resources))
	return resources, nil
}

func (c *Client) GetContact(ctx context.Context, bookHref, id string) (Resource, error) {
	requestURL := c.contactURL(bookHref, id)
	if requestURL == "" {
		return Resource{}, output.Validation("invalid_contact_id", "contact id is invalid", map[string]string{"id": id})
	}
	return c.get(ctx, requestURL)
}

func (c *Client) PutContact(ctx context.Context, bookHref, id, vcard string) (Resource, error) {
	if strings.TrimSpace(id) == "" {
		id = vcardUID(vcard)
	}
	if !strings.HasSuffix(id, ".vcf") {
		id += ".vcf"
	}
	requestURL := c.contactURL(bookHref, id)
	if requestURL == "" {
		return Resource{}, output.Validation("invalid_contact_id", "contact id is invalid", map[string]string{"id": id})
	}
	return c.put(ctx, requestURL, "text/vcard; charset=utf-8", vcard)
}

func (c *Client) DeleteContact(ctx context.Context, bookHref, id string) error {
	requestURL := c.contactURL(bookHref, id)
	if requestURL == "" {
		return output.Validation("invalid_contact_id", "contact id is invalid", map[string]string{"id": id})
	}
	return c.delete(ctx, requestURL)
}

func (c *Client) propfind(ctx context.Context, url string, body string, depth string) ([]Resource, error) {
	return c.xmlRequest(ctx, "PROPFIND", url, body, depth)
}

func (c *Client) report(ctx context.Context, url string, body string, depth string) ([]Resource, error) {
	return c.xmlRequest(ctx, "REPORT", url, body, depth)
}

func (c *Client) discoverHomeSet(ctx context.Context, wellKnownPath, principalBody, homeSetBody, homeSetName string) (string, error) {
	principalURL := c.childURL(c.BaseURL, wellKnownPath)
	logging.Info("webdav_discovery_start", "base_url", logging.SanitizedURL(c.BaseURL), "well_known_path", wellKnownPath, "home_set", homeSetName)
	resources, err := c.propfind(ctx, principalURL, principalBody, "0")
	if err != nil {
		return "", err
	}
	principalHref := firstPropHref(resources, "current-user-principal")
	if principalHref == "" {
		principalHref = firstPropHref(resources, "principal-URL")
	}
	if principalHref == "" {
		return "", output.Remote("webdav_discovery_failed", "iCloud WebDAV principal discovery returned no current-user-principal", map[string]string{"url": principalURL})
	}

	homeResources, err := c.propfind(ctx, c.resourceURL(principalHref), homeSetBody, "0")
	if err != nil {
		return "", err
	}
	homeHref := firstPropHref(homeResources, homeSetName)
	if homeHref == "" {
		return "", output.Remote("webdav_discovery_failed", "iCloud WebDAV home-set discovery returned no "+homeSetName, map[string]string{"principal": principalHref})
	}
	logging.Info("webdav_discovery_success", "home_set", homeSetName)
	return c.resourceURL(homeHref), nil
}

func (c *Client) xmlRequest(ctx context.Context, method string, requestURL string, body string, depth string) ([]Resource, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	if err := c.authorize(req); err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `application/xml; charset="utf-8"`)
	req.Header.Set("Depth", depth)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		logging.Error("webdav_request_transport_failed", "method", method, "url", logging.SanitizedURL(requestURL), "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return nil, err
	}
	defer resp.Body.Close()
	logging.Info("webdav_response", "method", method, "url", logging.SanitizedURL(requestURL), "status", resp.StatusCode, "duration_ms", time.Since(start).Milliseconds(), "request_bytes", len(body))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		logging.Warn("webdav_auth_failed", "method", method, "url", logging.SanitizedURL(requestURL), "status", resp.StatusCode)
		return nil, output.Auth("webdav_auth_failed", "iCloud WebDAV authentication failed", map[string]any{"status": resp.StatusCode})
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		logging.Error("webdav_request_failed", "method", method, "url", logging.SanitizedURL(requestURL), "status", resp.StatusCode)
		return nil, output.Remote("webdav_request_failed", "iCloud WebDAV request failed", map[string]any{"status": resp.StatusCode, "body": string(b)})
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		logging.Error("webdav_response_read_failed", "method", method, "url", logging.SanitizedURL(requestURL), "error", err.Error())
		return nil, err
	}
	resources, err := parseMultistatus(b)
	if err != nil {
		logging.Error("webdav_parse_failed", "method", method, "url", logging.SanitizedURL(requestURL), "response_bytes", len(b), "error", err.Error())
		return nil, err
	}
	logging.Info("webdav_multistatus_parsed", "method", method, "url", logging.SanitizedURL(requestURL), "response_bytes", len(b), "resource_count", len(resources))
	return resources, nil
}

func (c *Client) get(ctx context.Context, requestURL string) (Resource, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", requestURL, nil)
	if err != nil {
		return Resource{}, err
	}
	if err := c.authorize(req); err != nil {
		return Resource{}, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		logging.Error("webdav_get_transport_failed", "url", logging.SanitizedURL(requestURL), "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return Resource{}, err
	}
	defer resp.Body.Close()
	logging.Info("webdav_response", "method", "GET", "url", logging.SanitizedURL(requestURL), "status", resp.StatusCode, "duration_ms", time.Since(start).Milliseconds())
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		logging.Warn("webdav_auth_failed", "method", "GET", "url", logging.SanitizedURL(requestURL), "status", resp.StatusCode)
		return Resource{}, output.Auth("webdav_auth_failed", "iCloud WebDAV authentication failed", map[string]any{"status": resp.StatusCode})
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logging.Error("webdav_get_failed", "url", logging.SanitizedURL(requestURL), "status", resp.StatusCode)
		return Resource{}, output.Remote("webdav_get_failed", "iCloud WebDAV GET failed", map[string]any{"status": resp.StatusCode})
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		logging.Error("webdav_get_read_failed", "url", logging.SanitizedURL(requestURL), "error", err.Error())
		return Resource{}, err
	}
	logging.Info("webdav_get_success", "url", logging.SanitizedURL(requestURL), "response_bytes", len(b))
	return Resource{Href: requestURL, ETag: resp.Header.Get("ETag"), Data: string(b)}, nil
}

func (c *Client) put(ctx context.Context, requestURL string, contentType string, data string) (Resource, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "PUT", requestURL, strings.NewReader(data))
	if err != nil {
		return Resource{}, err
	}
	if err := c.authorize(req); err != nil {
		return Resource{}, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		logging.Error("webdav_put_transport_failed", "url", logging.SanitizedURL(requestURL), "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return Resource{}, err
	}
	defer resp.Body.Close()
	logging.Info("webdav_response", "method", "PUT", "url", logging.SanitizedURL(requestURL), "status", resp.StatusCode, "duration_ms", time.Since(start).Milliseconds(), "request_bytes", len(data))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		logging.Warn("webdav_auth_failed", "method", "PUT", "url", logging.SanitizedURL(requestURL), "status", resp.StatusCode)
		return Resource{}, output.Auth("webdav_auth_failed", "iCloud WebDAV authentication failed", map[string]any{"status": resp.StatusCode})
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		logging.Error("webdav_put_failed", "url", logging.SanitizedURL(requestURL), "status", resp.StatusCode)
		return Resource{}, output.Remote("webdav_put_failed", "iCloud WebDAV PUT failed", map[string]any{"status": resp.StatusCode, "body": string(b)})
	}
	logging.Info("webdav_put_success", "url", logging.SanitizedURL(requestURL), "request_bytes", len(data))
	return Resource{Href: requestURL, ETag: resp.Header.Get("ETag"), Data: data}, nil
}

func (c *Client) delete(ctx context.Context, requestURL string) error {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "DELETE", requestURL, nil)
	if err != nil {
		return err
	}
	if err := c.authorize(req); err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		logging.Error("webdav_delete_transport_failed", "url", logging.SanitizedURL(requestURL), "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return err
	}
	defer resp.Body.Close()
	logging.Info("webdav_response", "method", "DELETE", "url", logging.SanitizedURL(requestURL), "status", resp.StatusCode, "duration_ms", time.Since(start).Milliseconds())
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		logging.Warn("webdav_auth_failed", "method", "DELETE", "url", logging.SanitizedURL(requestURL), "status", resp.StatusCode)
		return output.Auth("webdav_auth_failed", "iCloud WebDAV authentication failed", map[string]any{"status": resp.StatusCode})
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logging.Error("webdav_delete_failed", "url", logging.SanitizedURL(requestURL), "status", resp.StatusCode)
		return output.Remote("webdav_delete_failed", "iCloud WebDAV DELETE failed", map[string]any{"status": resp.StatusCode})
	}
	logging.Info("webdav_delete_success", "url", logging.SanitizedURL(requestURL))
	return nil
}

// authorize attaches Basic Auth only after the request host is allowlisted.
func (c *Client) authorize(req *http.Request) error {
	if req == nil || req.URL == nil {
		return output.Validation("invalid_webdav_url", "WebDAV request URL is required", nil)
	}
	if err := c.ensureAllowedHost(req.URL); err != nil {
		return err
	}
	req.SetBasicAuth(c.Config.AppleID, c.Config.AppPassword)
	return nil
}

type multistatus struct {
	Responses []response `xml:"response"`
}

type response struct {
	Href     string     `xml:"href"`
	Propstat []propstat `xml:"propstat"`
}

type propstat struct {
	Prop prop `xml:"prop"`
}

type prop struct {
	DisplayName          string       `xml:"displayname"`
	ResourceType         resourceType `xml:"resourcetype"`
	ETag                 string       `xml:"getetag"`
	CalendarData         string       `xml:"calendar-data"`
	AddressData          string       `xml:"address-data"`
	CurrentUserPrincipal hrefProp     `xml:"current-user-principal"`
	PrincipalURL         hrefProp     `xml:"principal-URL"`
	CalendarHomeSet      hrefProp     `xml:"calendar-home-set"`
	AddressBookHomeSet   hrefProp     `xml:"addressbook-home-set"`
}

type resourceType struct {
	Any []xml.Name `xml:",any"`
}

type hrefProp struct {
	Href string `xml:"href"`
}

func parseMultistatus(b []byte) ([]Resource, error) {
	var ms multistatus
	if err := xml.Unmarshal(b, &ms); err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(ms.Responses))
	for _, r := range ms.Responses {
		res := Resource{Href: r.Href}
		for _, ps := range r.Propstat {
			if ps.Prop.DisplayName != "" {
				res.DisplayName = ps.Prop.DisplayName
			}
			if ps.Prop.ETag != "" {
				res.ETag = ps.Prop.ETag
			}
			if ps.Prop.CalendarData != "" {
				res.Data = ps.Prop.CalendarData
			}
			if ps.Prop.AddressData != "" {
				res.Data = ps.Prop.AddressData
			}
			addPropHref(&res, "current-user-principal", ps.Prop.CurrentUserPrincipal.Href)
			addPropHref(&res, "principal-URL", ps.Prop.PrincipalURL.Href)
			addPropHref(&res, "calendar-home-set", ps.Prop.CalendarHomeSet.Href)
			addPropHref(&res, "addressbook-home-set", ps.Prop.AddressBookHomeSet.Href)
			for _, name := range ps.Prop.ResourceType.Any {
				res.ResourceTypes = append(res.ResourceTypes, strings.TrimSpace(name.Local))
			}
		}
		if res.Href != "" {
			out = append(out, res)
		}
	}
	return out, nil
}

func (c *Client) resourceURL(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return c.BaseURL
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return c.BaseURL
	}
	ref, err := url.Parse(href)
	if err != nil {
		return c.BaseURL
	}
	return base.ResolveReference(ref).String()
}

// ensureAllowedHost rejects URLs that would send Basic Auth credentials to a
// non-allowlisted host (SSRF / credential exfiltration protection).
func (c *Client) ensureAllowedHost(u *url.URL) error {
	if u == nil {
		return output.Validation("invalid_webdav_url", "WebDAV URL is missing a host", nil)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return output.Validation("invalid_webdav_url", "WebDAV URL is missing a host", nil)
	}
	baseHost := ""
	if base, err := url.Parse(c.BaseURL); err == nil {
		baseHost = strings.ToLower(base.Hostname())
	}
	// Always allow the configured base host (production iCloud base or httptest in tests).
	if baseHost != "" && host == baseHost {
		return nil
	}
	if isAllowedICloudHost(host) {
		if !strings.EqualFold(u.Scheme, "https") {
			return output.Validation("insecure_webdav_url", "iCloud WebDAV URLs must use https", map[string]string{"host": host})
		}
		return nil
	}
	return output.Validation("webdav_host_not_allowed", "WebDAV URL host is not allowlisted for credentialed requests", map[string]string{"host": host})
}

func isAllowedICloudHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	switch host {
	case "caldav.icloud.com", "contacts.icloud.com", "icloud.com":
		return true
	}
	return strings.HasSuffix(host, ".icloud.com")
}

func (c *Client) childURL(parentHref, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return c.resourceURL(parentHref)
	}
	// Disallow path traversal. Multi-segment relative paths (e.g. .well-known/caldav) are OK.
	if containsPathTraversal(id) {
		return ""
	}
	u, err := url.Parse(c.resourceURL(parentHref))
	if err != nil {
		return c.resourceURL(parentHref)
	}
	u.Path = path.Join(u.Path, id)
	return u.String()
}

func containsPathTraversal(id string) bool {
	if strings.Contains(id, `\`) {
		return true
	}
	for _, part := range strings.Split(id, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func addPropHref(res *Resource, name, href string) {
	if strings.TrimSpace(href) == "" {
		return
	}
	if res.propHrefs == nil {
		res.propHrefs = map[string]string{}
	}
	res.propHrefs[name] = strings.TrimSpace(href)
}

func firstPropHref(resources []Resource, name string) string {
	for _, resource := range resources {
		if href := resource.propHrefs[name]; href != "" {
			return href
		}
	}
	return ""
}

func wrapRemote(code, message string, err error) error {
	var exitErr *output.ExitError
	if errors.As(err, &exitErr) {
		return err
	}
	return output.Remote(code, message, err.Error())
}

func isExitCode(err error, code string) bool {
	if err == nil {
		return false
	}
	var exitErr *output.ExitError
	return errors.As(err, &exitErr) && exitErr.Err.Code == code
}

func (c *Client) eventURL(calendarHref, id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "http://") || strings.HasPrefix(id, "https://") || strings.HasPrefix(id, "/") {
		return c.resourceURL(id)
	}
	// Bare resource names must not contain path separators or traversal.
	if containsPathTraversal(id) || strings.ContainsAny(id, `/\`) {
		return ""
	}
	if !strings.HasSuffix(id, ".ics") {
		id += ".ics"
	}
	return c.childURL(calendarHref, id)
}

func (c *Client) contactURL(bookHref, id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "http://") || strings.HasPrefix(id, "https://") || strings.HasPrefix(id, "/") {
		return c.resourceURL(id)
	}
	if containsPathTraversal(id) || strings.ContainsAny(id, `/\`) {
		return ""
	}
	if !strings.HasSuffix(id, ".vcf") {
		id += ".vcf"
	}
	return c.childURL(bookHref, id)
}

func calendarBody() string {
	return `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop>
    <D:displayname/>
    <D:resourcetype/>
    <C:supported-calendar-component-set/>
  </D:prop>
</D:propfind>`
}

func currentUserPrincipalBody() string {
	return `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:">
  <D:prop>
    <D:current-user-principal/>
    <D:principal-URL/>
  </D:prop>
</D:propfind>`
}

func calendarHomeSetBody() string {
	return `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop>
    <C:calendar-home-set/>
  </D:prop>
</D:propfind>`
}

func addressBookHomeSetBody() string {
	return `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
  <D:prop>
    <C:addressbook-home-set/>
  </D:prop>
</D:propfind>`
}

func addressBookBody() string {
	return `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
  <D:prop>
    <D:displayname/>
    <D:resourcetype/>
    <C:addressbook-description/>
  </D:prop>
</D:propfind>`
}

func calendarQueryBody(from, to string) string {
	timeRange := ""
	if from != "" || to != "" {
		timeRange = `<C:time-range start="` + escapeXMLAttr(compactCalTime(from)) + `" end="` + escapeXMLAttr(compactCalTime(to)) + `"/>`
	}
	return `<?xml version="1.0" encoding="utf-8"?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop>
    <D:getetag/>
    <C:calendar-data/>
  </D:prop>
  <C:filter>
    <C:comp-filter name="VCALENDAR">
      <C:comp-filter name="VEVENT">` + timeRange + `</C:comp-filter>
    </C:comp-filter>
  </C:filter>
</C:calendar-query>`
}

func addressBookQueryBody() string {
	return `<?xml version="1.0" encoding="utf-8"?>
<C:addressbook-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
  <D:prop>
    <D:getetag/>
    <C:address-data/>
  </D:prop>
</C:addressbook-query>`
}

func compactCalTime(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format("20060102T150405Z")
	}
	return strings.NewReplacer("-", "", ":", "", ".", "").Replace(s)
}

func escapeXMLAttr(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func eventUID(calendarData string) string {
	return firstLineValue(calendarData, "UID:", "event-"+time.Now().UTC().Format("20060102T150405Z"))
}

func vcardUID(vcard string) string {
	return firstLineValue(vcard, "UID:", "contact-"+time.Now().UTC().Format("20060102T150405Z"))
}

func firstLineValue(data, prefix, fallback string) string {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return fallback
}
