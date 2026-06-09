package webdav

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aaronfaby/icloud-cli/internal/config"
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
	return resources, nil
}

func (c *Client) ListEvents(ctx context.Context, calendarHref, from, to string) ([]Resource, error) {
	resources, err := c.report(ctx, c.resourceURL(calendarHref), calendarQueryBody(from, to), "1")
	if err != nil {
		return nil, wrapRemote("caldav_events_list_failed", "failed to list iCloud calendar events", err)
	}
	return resources, nil
}

func (c *Client) PutEvent(ctx context.Context, calendarHref, id, calendarData string) (Resource, error) {
	if strings.TrimSpace(id) == "" {
		id = eventUID(calendarData)
	}
	if !strings.HasSuffix(id, ".ics") {
		id += ".ics"
	}
	return c.put(ctx, c.eventURL(calendarHref, id), "text/calendar; charset=utf-8", calendarData)
}

func (c *Client) DeleteEvent(ctx context.Context, calendarHref, id string) error {
	return c.delete(ctx, c.eventURL(calendarHref, id))
}

func (c *Client) ListContacts(ctx context.Context, bookHref string) ([]Resource, error) {
	resources, err := c.report(ctx, c.resourceURL(bookHref), addressBookQueryBody(), "1")
	if err != nil {
		return nil, wrapRemote("carddav_contacts_list_failed", "failed to list iCloud contacts", err)
	}
	return resources, nil
}

func (c *Client) GetContact(ctx context.Context, bookHref, id string) (Resource, error) {
	return c.get(ctx, c.contactURL(bookHref, id))
}

func (c *Client) PutContact(ctx context.Context, bookHref, id, vcard string) (Resource, error) {
	if strings.TrimSpace(id) == "" {
		id = vcardUID(vcard)
	}
	if !strings.HasSuffix(id, ".vcf") {
		id += ".vcf"
	}
	return c.put(ctx, c.contactURL(bookHref, id), "text/vcard; charset=utf-8", vcard)
}

func (c *Client) DeleteContact(ctx context.Context, bookHref, id string) error {
	return c.delete(ctx, c.contactURL(bookHref, id))
}

func (c *Client) propfind(ctx context.Context, url string, body string, depth string) ([]Resource, error) {
	return c.xmlRequest(ctx, "PROPFIND", url, body, depth)
}

func (c *Client) report(ctx context.Context, url string, body string, depth string) ([]Resource, error) {
	return c.xmlRequest(ctx, "REPORT", url, body, depth)
}

func (c *Client) discoverHomeSet(ctx context.Context, wellKnownPath, principalBody, homeSetBody, homeSetName string) (string, error) {
	principalURL := c.childURL(c.BaseURL, wellKnownPath)
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
	return c.resourceURL(homeHref), nil
}

func (c *Client) xmlRequest(ctx context.Context, method string, url string, body string, depth string) ([]Resource, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.Config.AppleID, c.Config.AppPassword)
	req.Header.Set("Content-Type", `application/xml; charset="utf-8"`)
	req.Header.Set("Depth", depth)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, output.Auth("webdav_auth_failed", "iCloud WebDAV authentication failed", map[string]any{"status": resp.StatusCode})
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, output.Remote("webdav_request_failed", "iCloud WebDAV request failed", map[string]any{"status": resp.StatusCode, "body": string(b)})
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseMultistatus(b)
}

func (c *Client) get(ctx context.Context, url string) (Resource, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return Resource{}, err
	}
	req.SetBasicAuth(c.Config.AppleID, c.Config.AppPassword)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Resource{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return Resource{}, output.Auth("webdav_auth_failed", "iCloud WebDAV authentication failed", map[string]any{"status": resp.StatusCode})
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Resource{}, output.Remote("webdav_get_failed", "iCloud WebDAV GET failed", map[string]any{"status": resp.StatusCode})
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return Resource{}, err
	}
	return Resource{Href: url, ETag: resp.Header.Get("ETag"), Data: string(b)}, nil
}

func (c *Client) put(ctx context.Context, url string, contentType string, data string) (Resource, error) {
	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(data))
	if err != nil {
		return Resource{}, err
	}
	req.SetBasicAuth(c.Config.AppleID, c.Config.AppPassword)
	req.Header.Set("Content-Type", contentType)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Resource{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return Resource{}, output.Auth("webdav_auth_failed", "iCloud WebDAV authentication failed", map[string]any{"status": resp.StatusCode})
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Resource{}, output.Remote("webdav_put_failed", "iCloud WebDAV PUT failed", map[string]any{"status": resp.StatusCode, "body": string(b)})
	}
	return Resource{Href: url, ETag: resp.Header.Get("ETag"), Data: data}, nil
}

func (c *Client) delete(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.Config.AppleID, c.Config.AppPassword)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return output.Auth("webdav_auth_failed", "iCloud WebDAV authentication failed", map[string]any{"status": resp.StatusCode})
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return output.Remote("webdav_delete_failed", "iCloud WebDAV DELETE failed", map[string]any{"status": resp.StatusCode})
	}
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

func (c *Client) childURL(parentHref, id string) string {
	u, err := url.Parse(c.resourceURL(parentHref))
	if err != nil {
		return c.resourceURL(parentHref)
	}
	u.Path = path.Join(u.Path, id)
	return u.String()
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
	if _, ok := err.(*output.ExitError); ok {
		return err
	}
	return output.Remote(code, message, err.Error())
}

func isExitCode(err error, code string) bool {
	if err == nil {
		return false
	}
	exitErr, ok := err.(*output.ExitError)
	return ok && exitErr.Err.Code == code
}

func (c *Client) eventURL(calendarHref, id string) string {
	if strings.HasPrefix(id, "http://") || strings.HasPrefix(id, "https://") || strings.HasPrefix(id, "/") {
		return c.resourceURL(id)
	}
	if !strings.HasSuffix(id, ".ics") {
		id += ".ics"
	}
	return c.childURL(calendarHref, id)
}

func (c *Client) contactURL(bookHref, id string) string {
	if strings.HasPrefix(id, "http://") || strings.HasPrefix(id, "https://") || strings.HasPrefix(id, "/") {
		return c.resourceURL(id)
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
