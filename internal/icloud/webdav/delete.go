package webdav

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/aaronfaby/icloud-cli/internal/output"
)

// Deletion requires positive, namespace-qualified metadata. Listing's tolerant
// parser must not turn missing or failed properties into permission to delete.
type deleteMultistatus struct {
	XMLName   xml.Name `xml:"DAV: multistatus"`
	Responses []struct {
		Hrefs     []string `xml:"DAV: href"`
		Status    []string `xml:"DAV: status"`
		Propstats []struct {
			Status []string `xml:"DAV: status"`
			Props  []struct {
				Types []struct {
					Children []xml.Name `xml:",any"`
					Text     string     `xml:",chardata"`
				} `xml:"DAV: resourcetype"`
				ContentTypes []string `xml:"DAV: getcontenttype"`
				ETags        []string `xml:"DAV: getetag"`
			} `xml:"DAV: prop"`
		} `xml:"DAV: propstat"`
	} `xml:"DAV: response"`
}

func (c *Client) verifiedDeleteTarget(ctx context.Context, requestURL, contentType string) (string, string, error) {
	invalid := output.Validation("unsafe_delete_target", "delete requires a verified individual resource of the expected service with a strong ETag", nil)
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", requestURL, strings.NewReader(`<D:propfind xmlns:D="DAV:"><D:prop><D:resourcetype/><D:getcontenttype/><D:getetag/></D:prop></D:propfind>`))
	if err != nil {
		return "", "", err
	}
	if req.URL.Fragment != "" || req.URL.Path == "" || path.Clean(req.URL.Path) == "/" {
		return "", "", invalid
	}
	if err := c.authorize(req); err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "0")
	resp, err := c.do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", "", output.Auth("webdav_auth_failed", "iCloud WebDAV authentication failed", map[string]any{"status": resp.StatusCode})
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return "", "", output.Remote("delete_verification_failed", "WebDAV delete metadata request failed", map[string]any{"status": resp.StatusCode})
	}
	// Depth-zero resource metadata is small; never buffer an unbounded response.
	const maxMetadata = 64 << 10
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadata+1))
	if err != nil {
		return "", "", err
	}
	if len(data) > maxMetadata {
		return "", "", invalid
	}
	var ms deleteMultistatus
	decoder := xml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&ms); err != nil || len(ms.Responses) != 1 || resp.Request == nil || resp.Request.URL == nil {
		return "", "", invalid
	}
	if err := decoder.Decode(new(struct{})); err != io.EOF {
		return "", "", invalid
	}
	target := resp.Request.URL
	r := ms.Responses[0]
	if len(r.Hrefs) != 1 || (len(r.Status) > 1 || (len(r.Status) == 1 && !deletePropertyOK(r.Status[0]))) {
		return "", "", invalid
	}
	href, err := url.Parse(strings.TrimSpace(r.Hrefs[0]))
	if err != nil || strings.TrimSpace(r.Hrefs[0]) == "" {
		return "", "", invalid
	}
	resolved := target.ResolveReference(href)
	if resolved.String() != target.String() || target.Fragment != "" || target.Path == "" || path.Clean(target.Path) == "/" {
		return "", "", invalid
	}
	types, contentTypes, etags := 0, []string{}, []string{}
	for _, ps := range r.Propstats {
		if len(ps.Status) != 1 || !deletePropertyOK(ps.Status[0]) || len(ps.Props) != 1 {
			continue
		}
		p := ps.Props[0]
		for _, typ := range p.Types {
			if len(typ.Children) != 0 || strings.TrimSpace(typ.Text) != "" {
				return "", "", invalid
			}
			types++
		}
		contentTypes = append(contentTypes, p.ContentTypes...)
		etags = append(etags, p.ETags...)
	}
	if types != 1 || len(contentTypes) != 1 || len(etags) != 1 || !strongETag(etags[0]) {
		return "", "", invalid
	}
	actualType, _, err := mime.ParseMediaType(contentTypes[0])
	// RFC 6350 also identifies text/x-vcard as the older vCard media type.
	if contentType == "text/vcard" && actualType == "text/x-vcard" {
		actualType = "text/vcard"
	}
	if err != nil || actualType != contentType {
		return "", "", invalid
	}
	return target.String(), etags[0], nil
}

func deletePropertyOK(status string) bool {
	fields := strings.Fields(status)
	return len(fields) >= 2 && strings.HasPrefix(fields[0], "HTTP/") && fields[1] == "200"
}

func strongETag(etag string) bool {
	if len(etag) < 2 || etag[0] != '"' || etag[len(etag)-1] != '"' {
		return false
	}
	for _, b := range []byte(etag[1 : len(etag)-1]) {
		if b < 0x21 || b == '"' || b == 0x7f {
			return false
		}
	}
	return true
}
