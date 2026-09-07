package webdav

import (
	"context"
	"github.com/aaronfaby/icloud-cli/internal/output"
	"strconv"
	"strings"
)

type ContactSearch struct {
	Query, Name, Email, Phone, Organization string
	Limit                                   int
}
type ContactFields struct {
	FormattedName string   `json:"formatted_name,omitempty"`
	GivenName     string   `json:"given_name,omitempty"`
	FamilyName    string   `json:"family_name,omitempty"`
	Emails        []string `json:"emails,omitempty"`
	Phones        []string `json:"phones,omitempty"`
	Organization  string   `json:"organization,omitempty"`
}

func (c *Client) SearchContacts(ctx context.Context, book string, options ContactSearch) ([]Resource, error) {
	if options.Limit == 0 {
		options.Limit = 100
	}
	if options.Limit < 1 || options.Limit > 1000 {
		return nil, output.Validation("invalid_search_limit", "contact search limit must be between 1 and 1000", nil)
	}
	filters := []struct {
		value      string
		properties []string
	}{{options.Query, []string{"FN", "N", "EMAIL", "TEL", "ORG"}}, {options.Name, []string{"FN", "N"}}, {options.Email, []string{"EMAIL"}}, {options.Phone, []string{"TEL"}}, {options.Organization, []string{"ORG"}}}
	// CardDAV only allows one filter level. Require one search criterion so OR semantics are explicit.
	var selected []string
	value := ""
	for _, f := range filters {
		if f.value != "" {
			if value != "" {
				return nil, output.Validation("invalid_contact_search", "provide exactly one of query, name, email, phone, or organization", nil)
			}
			value = f.value
			selected = f.properties
		}
	}
	if strings.TrimSpace(value) == "" {
		return nil, output.Validation("missing_contact_search", "a contact search value is required", nil)
	}
	var body strings.Builder
	body.WriteString(`<C:addressbook-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav"><D:prop><D:getetag/><C:address-data/></D:prop><C:filter test="anyof">`)
	for _, property := range selected {
		body.WriteString(`<C:prop-filter name="` + property + `"><C:text-match collation="i;unicode-casemap" match-type="contains">` + escapeXMLAttr(value) + `</C:text-match></C:prop-filter>`)
	}
	body.WriteString(`</C:filter><C:limit><C:nresults>` + strconv.Itoa(options.Limit) + `</C:nresults></C:limit></C:addressbook-query>`)
	resources, err := c.report(ctx, c.resourceURL(book), body.String(), "1")
	if err != nil {
		return nil, err
	}
	if len(resources) > options.Limit {
		resources = resources[:options.Limit]
	}
	for i := range resources {
		fields, e := contactFields(resources[i].Data)
		if e != nil {
			return nil, e
		}
		resources[i].Contact = &fields
	}
	return resources, nil
}
func contactFields(data string) (ContactFields, error) {
	var result ContactFields
	lines, err := parseContent(data, "VCARD")
	if err != nil {
		return result, err
	}
	for _, line := range lines {
		if line.depth != 1 {
			continue
		}
		switch line.name {
		case "FN":
			result.FormattedName = unescapeText(line.value)
		case "N":
			parts := splitText(line.value, ';')
			if len(parts) > 0 {
				result.FamilyName = unescapeText(parts[0])
			}
			if len(parts) > 1 {
				result.GivenName = unescapeText(parts[1])
			}
		case "EMAIL":
			result.Emails = append(result.Emails, unescapeText(line.value))
		case "TEL":
			result.Phones = append(result.Phones, unescapeText(line.value))
		case "ORG":
			parts := splitText(line.value, ';')
			for i := range parts {
				parts[i] = unescapeText(parts[i])
			}
			result.Organization = strings.Join(parts, " / ")
		}
	}
	return result, nil
}
