package cli

import (
	"encoding/json"
	"github.com/aaronfaby/icloud-cli/internal/icloud/webdav"
	"github.com/aaronfaby/icloud-cli/internal/output"
	"strings"
)

func (a app) patchDAV(args []string, event bool) (any, error) {
	command, base, flagName := "contacts contacts patch", webdav.ContactsBase, "book"
	if event {
		command, base, flagName = "calendar events patch", webdav.CalendarBase, "calendar"
	}
	fs := newFlagSet(command)
	configPath := fs.String("config", "", "config path")
	collection := fs.String(flagName, "", "collection href")
	id := fs.String("id", "", "existing resource id or href")
	inputJSON := fs.String("input-json", "", "JSON patch or @path; null clears optional fields")
	if help, err := parseFlags(fs, args); help != nil || err != nil {
		return help, err
	}
	if strings.TrimSpace(*collection) == "" || strings.TrimSpace(*id) == "" {
		return nil, output.Validation("missing_patch_target", "collection and id are required", nil)
	}
	var patch map[string]json.RawMessage
	if err := a.decodeInput(*inputJSON, &patch); err != nil {
		return nil, err
	}
	client, ctx, cancel, err := a.webdavClient(*configPath, base)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if event {
		return client.PatchEvent(ctx, *collection, *id, patch)
	}
	return client.PatchContact(ctx, *collection, *id, patch)
}
func (a app) searchContacts(args []string) (any, error) {
	fs := newFlagSet("contacts contacts search")
	configPath := fs.String("config", "", "config path")
	book := fs.String("book", "", "address book href")
	var options webdav.ContactSearch
	fs.StringVar(&options.Query, "query", "", "match name, email, phone, or organization")
	fs.StringVar(&options.Name, "name", "", "match contact name")
	fs.StringVar(&options.Email, "email", "", "match email")
	fs.StringVar(&options.Phone, "phone", "", "match phone")
	fs.StringVar(&options.Organization, "organization", "", "match organization")
	fs.IntVar(&options.Limit, "limit", 100, "maximum results, 1-1000")
	if help, err := parseFlags(fs, args); help != nil || err != nil {
		return help, err
	}
	if options.Limit < 1 || options.Limit > 1000 {
		return nil, output.Validation("invalid_search_limit", "contact search limit must be between 1 and 1000", nil)
	}
	if strings.TrimSpace(*book) == "" {
		return nil, output.Validation("missing_book", "address book href is required", nil)
	}
	client, ctx, cancel, err := a.webdavClient(*configPath, webdav.ContactsBase)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return client.SearchContacts(ctx, *book, options)
}
