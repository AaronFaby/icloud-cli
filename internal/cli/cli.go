package cli

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aaronfaby/icloud-cli/internal/config"
	"github.com/aaronfaby/icloud-cli/internal/icloud"
	"github.com/aaronfaby/icloud-cli/internal/icloud/mail"
	"github.com/aaronfaby/icloud-cli/internal/icloud/webdav"
	"github.com/aaronfaby/icloud-cli/internal/logging"
	"github.com/aaronfaby/icloud-cli/internal/output"
)

type app struct {
	args   []string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

type eventInput struct {
	ID           string `json:"id,omitempty"`
	UID          string `json:"uid,omitempty"`
	Summary      string `json:"summary,omitempty"`
	Description  string `json:"description,omitempty"`
	Location     string `json:"location,omitempty"`
	Start        string `json:"start,omitempty"`
	End          string `json:"end,omitempty"`
	CalendarData string `json:"calendar_data,omitempty"`
}

type contactInput struct {
	ID            string   `json:"id,omitempty"`
	UID           string   `json:"uid,omitempty"`
	FormattedName string   `json:"formatted_name,omitempty"`
	GivenName     string   `json:"given_name,omitempty"`
	FamilyName    string   `json:"family_name,omitempty"`
	Emails        []string `json:"emails,omitempty"`
	Phones        []string `json:"phones,omitempty"`
	Organization  string   `json:"organization,omitempty"`
	Note          string   `json:"note,omitempty"`
	VCard         string   `json:"vcard,omitempty"`
}

func Run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	logging.Configure(logging.LoadConfig(), stderr)
	args = normalizeArgs(args)
	a := app{args: args, stdin: stdin, stdout: stdout, stderr: stderr}
	service, operation := classify(args)
	start := time.Now()
	logging.Info("command_start", "service", service, "operation", operation, "args", logging.SanitizedArgs(args))
	data, err := a.dispatch()
	if err != nil {
		code := output.Failure(stdout, service, operation, err)
		logFailure(code, service, operation, start, err)
		return code
	}
	code := output.Success(stdout, service, operation, data)
	logging.Info("command_success", "service", service, "operation", operation, "exit_code", code, "duration_ms", time.Since(start).Milliseconds())
	return code
}

func (a app) dispatch() (any, error) {
	if len(a.args) == 0 {
		return usage(), nil
	}
	switch a.args[0] {
	case "help", "--help", "-h":
		return usage(), nil
	case "auth":
		return a.auth(a.args[1:])
	case "services":
		return a.services(a.args[1:])
	case "log":
		return a.log(a.args[1:])
	case "mail":
		return a.mail(a.args[1:])
	case "calendar":
		return a.calendar(a.args[1:])
	case "contacts":
		return a.contacts(a.args[1:])
	case "drive", "icloud-drive", "icloud_drive", "notes", "reminders", "photos":
		return nil, output.Unsupported(a.args[0], "this iCloud service is listed but not implemented in the documented-protocol v1")
	default:
		return nil, output.Validation("unknown_command", "unknown command", map[string]string{"command": a.args[0]})
	}
}

func (a app) log(args []string) (any, error) {
	if len(args) == 0 {
		return nil, output.Validation("missing_log_command", "log command is required", nil)
	}
	switch args[0] {
	case "status":
		fs := newFlagSet("log status")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		return logging.CurrentStatus(), nil
	default:
		return nil, output.Validation("unknown_log_command", "unknown log command", map[string]string{"command": args[0]})
	}
}

func (a app) auth(args []string) (any, error) {
	if len(args) == 0 {
		return nil, output.Validation("missing_auth_command", "auth command is required", nil)
	}
	switch args[0] {
	case "check":
		fs := newFlagSet("auth check")
		configPath := fs.String("config", "", "config path")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		cfg, report, err := config.RequireCredentials(*configPath)
		if err != nil {
			return nil, err
		}
		return config.Redacted(cfg, report), nil
	case "save":
		fs := newFlagSet("auth save")
		configPath := fs.String("config", "", "config path")
		appleID := fs.String("apple-id", "", "Apple ID")
		appPassword := fs.String("app-password", "", "app-specific password")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		path, err := config.Save(config.SaveOptions{Path: *configPath, AppleID: *appleID, AppPassword: *appPassword})
		if err != nil {
			return nil, err
		}
		return map[string]any{"config_path": path, "saved": true, "warning": "credentials are stored in plaintext JSON"}, nil
	case "doctor":
		fs := newFlagSet("auth doctor")
		configPath := fs.String("config", "", "config path")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		cfg, report, err := config.Load(*configPath)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"credentials":      config.Redacted(cfg, report),
			"imap_endpoint":    mail.DefaultIMAPHost,
			"smtp_endpoint":    mail.DefaultSMTPHost,
			"caldav_endpoint":  webdav.CalendarBase,
			"carddav_endpoint": webdav.ContactsBase,
		}, nil
	default:
		return nil, output.Validation("unknown_auth_command", "unknown auth command", map[string]string{"command": args[0]})
	}
}

func (a app) services(args []string) (any, error) {
	if len(args) == 0 {
		return nil, output.Validation("missing_services_command", "services command is required", nil)
	}
	switch args[0] {
	case "list", "capabilities":
		return icloud.Capabilities(), nil
	default:
		return nil, output.Validation("unknown_services_command", "unknown services command", map[string]string{"command": args[0]})
	}
}

func (a app) mail(args []string) (any, error) {
	if len(args) < 2 {
		return nil, output.Validation("missing_mail_command", "mail command group and operation are required", nil)
	}
	switch args[0] {
	case "folders":
		return a.mailFolders(args[1:])
	case "messages":
		return a.mailMessages(args[1:])
	case "batch":
		return a.mailBatch(args[1:])
	default:
		return nil, output.Validation("unknown_mail_group", "unknown mail command group", map[string]string{"group": args[0]})
	}
}

func (a app) mailFolders(args []string) (any, error) {
	switch args[0] {
	case "list":
		fs := newFlagSet("mail folders list")
		configPath := fs.String("config", "", "config path")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		client, err := a.imapClient(*configPath)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		return client.ListFolders()
	case "create":
		fs := newFlagSet("mail folders create")
		configPath := fs.String("config", "", "config path")
		name := fs.String("name", "", "folder name")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		client, err := a.imapClient(*configPath)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		if err := client.CreateFolder(*name); err != nil {
			return nil, err
		}
		return map[string]any{"folder": *name, "created": true}, nil
	case "rename":
		fs := newFlagSet("mail folders rename")
		configPath := fs.String("config", "", "config path")
		folder := fs.String("folder", "", "source folder")
		name := fs.String("name", "", "new folder name")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		client, err := a.imapClient(*configPath)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		if err := client.RenameFolder(*folder, *name); err != nil {
			return nil, err
		}
		return map[string]any{"folder": *folder, "new_name": *name, "renamed": true}, nil
	case "delete":
		fs := newFlagSet("mail folders delete")
		configPath := fs.String("config", "", "config path")
		folder := fs.String("folder", "", "folder")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		client, err := a.imapClient(*configPath)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		if err := client.DeleteFolder(*folder); err != nil {
			return nil, err
		}
		return map[string]any{"folder": *folder, "deleted": true}, nil
	default:
		return nil, output.Validation("unknown_mail_folder_command", "unknown mail folders command", map[string]string{"command": args[0]})
	}
}

func (a app) mailMessages(args []string) (any, error) {
	switch args[0] {
	case "list":
		fs := newFlagSet("mail messages list")
		configPath := fs.String("config", "", "config path")
		folder := fs.String("folder", "INBOX", "folder")
		limit := fs.Int("limit", 25, "message limit")
		unread := fs.Bool("unread", false, "only unread messages")
		flagged := fs.Bool("flagged", false, "only flagged messages")
		since := fs.String("since", "", "only messages since duration, RFC3339 time, or YYYY-MM-DD date")
		from := fs.String("from", "", "from address or domain text")
		rawHeaders := fs.Bool("raw-headers", false, "include raw header fields")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		sinceCriteria, err := parseMailSince(*since, time.Now)
		if err != nil {
			return nil, err
		}
		client, err := a.imapClient(*configPath)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		return client.ListMessagesWithOptions(mail.MessageListOptions{
			Folder:     *folder,
			Limit:      *limit,
			Unread:     *unread,
			Flagged:    *flagged,
			Since:      sinceCriteria,
			From:       *from,
			RawHeaders: *rawHeaders,
		})
	case "get":
		fs := newFlagSet("mail messages get")
		configPath := fs.String("config", "", "config path")
		folder := fs.String("folder", "INBOX", "folder")
		id := fs.String("id", "", "message UID")
		raw := fs.Bool("raw", false, "include raw RFC822 message")
		body := fs.String("body", "", "extract body as text or html")
		attachments := fs.Bool("attachments", false, "include attachment metadata")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		bodyMode, err := mailBodyMode(*body)
		if err != nil {
			return nil, err
		}
		client, err := a.imapClient(*configPath)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		return client.FetchMessageWithOptions(*folder, *id, mail.FetchOptions{IncludeRaw: *raw, BodyMode: bodyMode, IncludeAttachments: *attachments})
	case "attachment":
		return a.mailMessageAttachment(args[1:])
	case "search":
		fs := newFlagSet("mail messages search")
		configPath := fs.String("config", "", "config path")
		folder := fs.String("folder", "INBOX", "folder")
		query := fs.String("query", "ALL", "IMAP criteria or text query")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		client, err := a.imapClient(*configPath)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		ids, err := client.Search(*folder, *query)
		if err != nil {
			return nil, err
		}
		return map[string]any{"folder": *folder, "query": *query, "ids": ids, "count": len(ids)}, nil
	case "send":
		fs := newFlagSet("mail send")
		configPath := fs.String("config", "", "config path")
		inputJSON := fs.String("input-json", "", "JSON request or @path")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		cfg, _, err := config.RequireCredentials(*configPath)
		if err != nil {
			return nil, err
		}
		var req mail.SendRequest
		if err := a.decodeInput(*inputJSON, &req); err != nil {
			return nil, err
		}
		return mail.Send(cfg, req)
	case "reply", "reply-all", "forward":
		return a.mailMessageResponse(args)
	case "move", "copy", "delete", "archive", "flag", "unflag", "mark-read", "mark-unread":
		return a.singleMailMutation(args)
	default:
		return nil, output.Validation("unknown_mail_message_command", "unknown mail messages command", map[string]string{"command": args[0]})
	}
}

func (a app) mailMessageAttachment(args []string) (any, error) {
	if len(args) == 0 {
		return nil, output.Validation("missing_attachment_command", "mail messages attachment command is required", nil)
	}
	switch args[0] {
	case "get":
		fs := newFlagSet("mail messages attachment get")
		configPath := fs.String("config", "", "config path")
		folder := fs.String("folder", "INBOX", "folder")
		id := fs.String("id", "", "message UID")
		attachmentID := fs.String("attachment", "", "attachment id from messages get --attachments")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		client, err := a.imapClient(*configPath)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		return client.FetchAttachment(*folder, *id, *attachmentID)
	default:
		return nil, output.Validation("unknown_attachment_command", "unknown mail messages attachment command", map[string]string{"command": args[0]})
	}
}

func mailBodyMode(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case "", "text", "html":
		return mode, nil
	default:
		return "", output.Validation("invalid_body_mode", "body must be text or html", map[string]string{"body": value})
	}
}

func (a app) mailMessageResponse(args []string) (any, error) {
	op := args[0]
	fs := newFlagSet("mail messages " + op)
	configPath := fs.String("config", "", "config path")
	folder := fs.String("folder", "INBOX", "folder")
	id := fs.String("id", "", "message UID")
	inputJSON := fs.String("input-json", "", "JSON request or @path")
	dryRun := fs.Bool("dry-run", false, "preview without sending or saving")
	draft := fs.Bool("draft", false, "save composed message to Drafts instead of sending")
	if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
		return help, err
	}
	if strings.TrimSpace(*id) == "" {
		return nil, output.Validation("missing_message_id", "message id is required", nil)
	}
	cfg, _, err := config.RequireCredentials(*configPath)
	if err != nil {
		return nil, err
	}
	var input mail.ResponseInput
	if err := a.decodeInput(*inputJSON, &input); err != nil {
		return nil, err
	}
	client, err := a.imapClient(*configPath)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	source, err := client.FetchMessageWithOptions(*folder, *id, mail.FetchOptions{IncludeRaw: true, RawHeaders: true})
	if err != nil {
		return nil, err
	}
	action := "send"
	if *draft {
		action = "create_draft"
	}
	prepared, err := mail.PrepareResponse(cfg, source, mail.ResponseKind(op), input, action)
	if err != nil {
		return nil, err
	}
	if *dryRun {
		return prepared, nil
	}
	if *draft {
		draftResult, err := mail.AppendDraft(client, prepared.Request)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"action":        prepared.Action,
			"from":          prepared.From,
			"to":            prepared.To,
			"cc":            prepared.CC,
			"bcc_count":     len(prepared.BCC),
			"subject":       prepared.Subject,
			"headers":       prepared.Headers,
			"source_folder": prepared.SourceFolder,
			"source_id":     prepared.SourceID,
			"draft":         draftResult,
		}, nil
	}
	sendResult, err := mail.Send(cfg, prepared.Request)
	if err != nil {
		return nil, err
	}
	sourceFlag := map[string]any{"ok": true, "flag": prepared.SourceFlag}
	if prepared.SourceFlag != "" {
		if err := client.SetFlag(prepared.SourceFolder, prepared.SourceID, prepared.SourceFlag, true); err != nil {
			sourceFlag["ok"] = false
			sourceFlag["error"] = err.Error()
		}
	}
	sendResult["action"] = prepared.Action
	sendResult["headers"] = prepared.Headers
	sendResult["source_folder"] = prepared.SourceFolder
	sendResult["source_id"] = prepared.SourceID
	sendResult["source_flag"] = sourceFlag
	return sendResult, nil
}

func (a app) singleMailMutation(args []string) (any, error) {
	op := args[0]
	fs := newFlagSet("mail messages " + op)
	configPath := fs.String("config", "", "config path")
	folder := fs.String("folder", "INBOX", "folder")
	id := fs.String("id", "", "message UID")
	toFolder := fs.String("to-folder", "", "destination folder")
	trashFolder := fs.String("trash-folder", mail.DefaultTrash, "trash folder")
	archiveFolder := fs.String("archive-folder", mail.DefaultArchive, "archive folder")
	permanent := fs.Bool("permanent", false, "permanently delete")
	dryRun := fs.Bool("dry-run", false, "preview mutation")
	if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
		return help, err
	}
	if strings.TrimSpace(*id) == "" {
		return nil, output.Validation("missing_message_id", "message id is required", nil)
	}
	if *dryRun && op != "delete" {
		return mail.MutationResult{ID: *id, OK: true, Warning: "dry_run:" + op}, nil
	}
	client, err := a.imapClient(*configPath)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	result := mail.MutationResult{ID: *id, OK: true}
	switch op {
	case "move":
		err = client.Move(*folder, *id, *toFolder)
	case "copy":
		err = client.Copy(*folder, *id, *toFolder)
	case "delete":
		result, err = client.Delete(*folder, *id, *trashFolder, *permanent, *dryRun)
	case "archive":
		err = client.Archive(*folder, *id, *archiveFolder)
	case "flag":
		err = client.SetFlag(*folder, *id, `\Flagged`, true)
	case "unflag":
		err = client.SetFlag(*folder, *id, `\Flagged`, false)
	case "mark-read":
		err = client.SetFlag(*folder, *id, `\Seen`, true)
	case "mark-unread":
		err = client.SetFlag(*folder, *id, `\Seen`, false)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (a app) mailBatch(args []string) (any, error) {
	op := args[0]
	fs := newFlagSet("mail batch " + op)
	configPath := fs.String("config", "", "config path")
	inputJSON := fs.String("input-json", "", "JSON request or @path")
	if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
		return help, err
	}
	var req mail.BatchRequest
	if err := a.decodeInput(*inputJSON, &req); err != nil {
		return nil, err
	}
	if req.Folder == "" {
		req.Folder = "INBOX"
	}
	if len(req.IDs) == 0 {
		return nil, output.Validation("missing_message_ids", "batch request must include ids", nil)
	}
	if req.DryRun && op != "delete" {
		results := make([]mail.MutationResult, 0, len(req.IDs))
		for _, id := range req.IDs {
			results = append(results, mail.MutationResult{ID: id, OK: true, Warning: "dry_run:" + op})
		}
		return map[string]any{"folder": req.Folder, "operation": op, "results": results}, nil
	}
	client, err := a.imapClient(*configPath)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	results := make([]mail.MutationResult, 0, len(req.IDs))
	for _, id := range req.IDs {
		result := mail.MutationResult{ID: id, OK: true}
		switch op {
		case "move":
			err = client.Move(req.Folder, id, req.ToFolder)
		case "copy":
			err = client.Copy(req.Folder, id, req.ToFolder)
		case "delete":
			result, err = client.Delete(req.Folder, id, req.TrashFolder, req.Permanent, req.DryRun)
		case "flag":
			err = client.SetFlag(req.Folder, id, `\Flagged`, true)
		case "unflag":
			err = client.SetFlag(req.Folder, id, `\Flagged`, false)
		case "mark-read":
			err = client.SetFlag(req.Folder, id, `\Seen`, true)
		case "mark-unread":
			err = client.SetFlag(req.Folder, id, `\Seen`, false)
		default:
			return nil, output.Validation("unknown_mail_batch_command", "unknown mail batch command", map[string]string{"command": op})
		}
		if err != nil {
			result.OK = false
			result.Error = err.Error()
			err = nil
		}
		results = append(results, result)
	}
	return map[string]any{"folder": req.Folder, "operation": op, "results": results}, nil
}

func (a app) calendar(args []string) (any, error) {
	if len(args) < 2 {
		return nil, output.Validation("missing_calendar_command", "calendar command group and operation are required", nil)
	}
	if args[0] == "calendars" && args[1] == "list" {
		fs := newFlagSet("calendar calendars list")
		configPath := fs.String("config", "", "config path")
		if help, err := parseFlags(fs, args[2:]); help != nil || err != nil {
			return help, err
		}
		cfg, _, err := config.RequireCredentials(*configPath)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		return webdav.New(webdav.CalendarBase, cfg).ListCalendars(ctx)
	}
	if args[0] == "events" {
		return a.calendarEvents(args[1:])
	}
	return nil, output.Validation("unknown_calendar_command", "unknown calendar command", map[string]string{"group": args[0], "command": args[1]})
}

func (a app) calendarEvents(args []string) (any, error) {
	switch args[0] {
	case "list":
		fs := newFlagSet("calendar events list")
		configPath := fs.String("config", "", "config path")
		calendarHref := fs.String("calendar", "", "calendar href from calendars list")
		calendarName := fs.String("calendar-name", "", "calendar display name from calendars list")
		from := fs.String("from", "", "RFC3339 or CalDAV UTC start")
		to := fs.String("to", "", "RFC3339 or CalDAV UTC end")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		if strings.TrimSpace(*calendarHref) != "" && strings.TrimSpace(*calendarName) != "" {
			return nil, output.Validation("ambiguous_calendar", "calendar and calendar-name are mutually exclusive", nil)
		}
		client, ctx, cancel, err := a.webdavClient(*configPath, webdav.CalendarBase)
		if err != nil {
			return nil, err
		}
		defer cancel()
		href := strings.TrimSpace(*calendarHref)
		if href == "" {
			href, err = resolveCalendarName(ctx, client, *calendarName)
			if err != nil {
				return nil, err
			}
		}
		return client.ListEvents(ctx, href, *from, *to)
	case "create", "update":
		fs := newFlagSet("calendar events " + args[0])
		configPath := fs.String("config", "", "config path")
		calendarHref := fs.String("calendar", "", "calendar href from calendars list")
		id := fs.String("id", "", "event resource id or href")
		inputJSON := fs.String("input-json", "", "JSON request or @path")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		if strings.TrimSpace(*calendarHref) == "" {
			return nil, output.Validation("missing_calendar", "calendar href is required", nil)
		}
		var input eventInput
		if err := a.decodeInput(*inputJSON, &input); err != nil {
			return nil, err
		}
		if strings.TrimSpace(*id) == "" {
			*id = input.ID
		}
		payload, err := buildCalendarData(input)
		if err != nil {
			return nil, err
		}
		client, ctx, cancel, err := a.webdavClient(*configPath, webdav.CalendarBase)
		if err != nil {
			return nil, err
		}
		defer cancel()
		return client.PutEvent(ctx, *calendarHref, *id, payload)
	case "delete":
		fs := newFlagSet("calendar events delete")
		configPath := fs.String("config", "", "config path")
		calendarHref := fs.String("calendar", "", "calendar href from calendars list")
		id := fs.String("id", "", "event resource id or href")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		if strings.TrimSpace(*calendarHref) == "" || strings.TrimSpace(*id) == "" {
			return nil, output.Validation("missing_calendar_event", "calendar and id are required", nil)
		}
		client, ctx, cancel, err := a.webdavClient(*configPath, webdav.CalendarBase)
		if err != nil {
			return nil, err
		}
		defer cancel()
		if err := client.DeleteEvent(ctx, *calendarHref, *id); err != nil {
			return nil, err
		}
		return map[string]any{"id": *id, "deleted": true}, nil
	default:
		return nil, output.Validation("unknown_calendar_events_command", "unknown calendar events command", map[string]string{"command": args[0]})
	}
}

func (a app) contacts(args []string) (any, error) {
	if len(args) < 2 {
		return nil, output.Validation("missing_contacts_command", "contacts command group and operation are required", nil)
	}
	if args[0] == "books" && args[1] == "list" {
		fs := newFlagSet("contacts books list")
		configPath := fs.String("config", "", "config path")
		if help, err := parseFlags(fs, args[2:]); help != nil || err != nil {
			return help, err
		}
		cfg, _, err := config.RequireCredentials(*configPath)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		return webdav.New(webdav.ContactsBase, cfg).ListAddressBooks(ctx)
	}
	if args[0] == "contacts" {
		return a.contactResources(args[1:])
	}
	return nil, output.Validation("unknown_contacts_command", "unknown contacts command", map[string]string{"group": args[0], "command": args[1]})
}

func (a app) contactResources(args []string) (any, error) {
	switch args[0] {
	case "list":
		fs := newFlagSet("contacts contacts list")
		configPath := fs.String("config", "", "config path")
		bookHref := fs.String("book", "", "address book href from books list")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		if strings.TrimSpace(*bookHref) == "" {
			return nil, output.Validation("missing_book", "address book href is required", nil)
		}
		client, ctx, cancel, err := a.webdavClient(*configPath, webdav.ContactsBase)
		if err != nil {
			return nil, err
		}
		defer cancel()
		return client.ListContacts(ctx, *bookHref)
	case "get":
		fs := newFlagSet("contacts contacts get")
		configPath := fs.String("config", "", "config path")
		bookHref := fs.String("book", "", "address book href from books list")
		id := fs.String("id", "", "contact resource id or href")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		if strings.TrimSpace(*bookHref) == "" || strings.TrimSpace(*id) == "" {
			return nil, output.Validation("missing_contact", "book and id are required", nil)
		}
		client, ctx, cancel, err := a.webdavClient(*configPath, webdav.ContactsBase)
		if err != nil {
			return nil, err
		}
		defer cancel()
		return client.GetContact(ctx, *bookHref, *id)
	case "create", "update":
		fs := newFlagSet("contacts contacts " + args[0])
		configPath := fs.String("config", "", "config path")
		bookHref := fs.String("book", "", "address book href from books list")
		id := fs.String("id", "", "contact resource id or href")
		inputJSON := fs.String("input-json", "", "JSON request or @path")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		if strings.TrimSpace(*bookHref) == "" {
			return nil, output.Validation("missing_book", "address book href is required", nil)
		}
		var input contactInput
		if err := a.decodeInput(*inputJSON, &input); err != nil {
			return nil, err
		}
		if strings.TrimSpace(*id) == "" {
			*id = input.ID
		}
		payload, err := buildVCard(input)
		if err != nil {
			return nil, err
		}
		client, ctx, cancel, err := a.webdavClient(*configPath, webdav.ContactsBase)
		if err != nil {
			return nil, err
		}
		defer cancel()
		return client.PutContact(ctx, *bookHref, *id, payload)
	case "delete":
		fs := newFlagSet("contacts contacts delete")
		configPath := fs.String("config", "", "config path")
		bookHref := fs.String("book", "", "address book href from books list")
		id := fs.String("id", "", "contact resource id or href")
		if help, err := parseFlags(fs, args[1:]); help != nil || err != nil {
			return help, err
		}
		if strings.TrimSpace(*bookHref) == "" || strings.TrimSpace(*id) == "" {
			return nil, output.Validation("missing_contact", "book and id are required", nil)
		}
		client, ctx, cancel, err := a.webdavClient(*configPath, webdav.ContactsBase)
		if err != nil {
			return nil, err
		}
		defer cancel()
		if err := client.DeleteContact(ctx, *bookHref, *id); err != nil {
			return nil, err
		}
		return map[string]any{"id": *id, "deleted": true}, nil
	default:
		return nil, output.Validation("unknown_contacts_contacts_command", "unknown contacts command", map[string]string{"command": args[0]})
	}
}

func (a app) imapClient(configPath string) (*mail.IMAPClient, error) {
	cfg, _, err := config.RequireCredentials(configPath)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return mail.DialIMAP(ctx, cfg)
}

func (a app) webdavClient(configPath string, baseURL string) (*webdav.Client, context.Context, context.CancelFunc, error) {
	cfg, _, err := config.RequireCredentials(configPath)
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	return webdav.New(baseURL, cfg), ctx, cancel, nil
}

func (a app) decodeInput(input string, v any) error {
	var b []byte
	var err error
	source := "stdin"
	switch {
	case strings.HasPrefix(input, "@"):
		source = "file"
		b, err = os.ReadFile(strings.TrimPrefix(input, "@"))
	case strings.TrimSpace(input) != "":
		source = "argument"
		b = []byte(input)
	default:
		b, err = io.ReadAll(a.stdin)
	}
	if err != nil {
		return output.Validation("input_read_failed", "failed to read JSON input", err.Error())
	}
	if strings.TrimSpace(string(b)) == "" {
		return output.Validation("missing_input_json", "JSON input is required", nil)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return output.Validation("invalid_input_json", "failed to parse JSON input", err.Error())
	}
	logging.Info("json_input_decoded", "source", source, "bytes", len(b))
	return nil
}

func classify(args []string) (string, string) {
	if len(args) == 0 {
		return "cli", "help"
	}
	service := args[0]
	var opParts []string
	for _, arg := range args[1:] {
		if strings.HasPrefix(arg, "-") {
			break
		}
		opParts = append(opParts, arg)
	}
	operation := strings.Join(opParts, ".")
	if operation == "" {
		operation = "help"
	}
	return service, operation
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func normalizeArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--json" || arg == "-json" || strings.HasPrefix(arg, "--json=") || strings.HasPrefix(arg, "-json=") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func parseFlags(fs *flag.FlagSet, args []string) (any, error) {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return commandHelp(fs), nil
		}
		return nil, err
	}
	return nil, nil
}

func commandHelp(fs *flag.FlagSet) map[string]any {
	flags := []map[string]any{}
	fs.VisitAll(func(f *flag.Flag) {
		flags = append(flags, map[string]any{
			"name":    f.Name,
			"usage":   f.Usage,
			"default": f.DefValue,
		})
	})
	return map[string]any{
		"usage": "icloud " + fs.Name() + " [flags]",
		"flags": flags,
	}
}

func parseMailSince(s string, now func() time.Time) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now().Add(-d).Format("02-Jan-2006"), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("02-Jan-2006"), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Format("02-Jan-2006"), nil
	}
	return "", output.Validation("invalid_since", "since must be a duration, RFC3339 time, or YYYY-MM-DD date", map[string]string{"since": s})
}

func resolveCalendarName(ctx context.Context, client *webdav.Client, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", output.Validation("missing_calendar", "calendar href or calendar-name is required", nil)
	}
	calendars, err := client.ListCalendars(ctx)
	if err != nil {
		return "", err
	}
	var matches []webdav.Resource
	for _, calendar := range calendars {
		if strings.EqualFold(strings.TrimSpace(calendar.DisplayName), name) {
			matches = append(matches, calendar)
		}
	}
	switch len(matches) {
	case 0:
		return "", output.Validation("calendar_not_found", "calendar name was not found", map[string]string{"calendar_name": name})
	case 1:
		return matches[0].Href, nil
	default:
		return "", output.Validation("ambiguous_calendar_name", "calendar name matched multiple calendars", map[string]any{"calendar_name": name, "matches": len(matches)})
	}
}

func buildCalendarData(input eventInput) (string, error) {
	if strings.TrimSpace(input.CalendarData) != "" {
		return normalizeLines(input.CalendarData), nil
	}
	uid := firstNonEmpty(input.UID, input.ID, "event-"+time.Now().UTC().Format("20060102T150405Z"))
	if strings.TrimSpace(input.Summary) == "" {
		return "", output.Validation("missing_event_summary", "event summary is required when calendar_data is not provided", nil)
	}
	if strings.TrimSpace(input.Start) == "" || strings.TrimSpace(input.End) == "" {
		return "", output.Validation("missing_event_time", "event start and end are required when calendar_data is not provided", nil)
	}
	lines := []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//icloud-cli//agentic iCloud CLI//EN",
		"BEGIN:VEVENT",
		"UID:" + escapeICal(uid),
		"DTSTAMP:" + time.Now().UTC().Format("20060102T150405Z"),
		"DTSTART:" + formatICalTime(input.Start),
		"DTEND:" + formatICalTime(input.End),
		"SUMMARY:" + escapeICal(input.Summary),
	}
	if strings.TrimSpace(input.Description) != "" {
		lines = append(lines, "DESCRIPTION:"+escapeICal(input.Description))
	}
	if strings.TrimSpace(input.Location) != "" {
		lines = append(lines, "LOCATION:"+escapeICal(input.Location))
	}
	lines = append(lines, "END:VEVENT", "END:VCALENDAR", "")
	return strings.Join(lines, "\r\n"), nil
}

func buildVCard(input contactInput) (string, error) {
	if strings.TrimSpace(input.VCard) != "" {
		return normalizeLines(input.VCard), nil
	}
	fn := strings.TrimSpace(input.FormattedName)
	if fn == "" {
		fn = strings.TrimSpace(strings.Join([]string{input.GivenName, input.FamilyName}, " "))
	}
	if fn == "" {
		return "", output.Validation("missing_contact_name", "formatted_name is required when vcard is not provided", nil)
	}
	uid := firstNonEmpty(input.UID, input.ID, "contact-"+time.Now().UTC().Format("20060102T150405Z"))
	lines := []string{
		"BEGIN:VCARD",
		"VERSION:3.0",
		"PRODID:-//icloud-cli//agentic iCloud CLI//EN",
		"UID:" + escapeVCard(uid),
		"N:" + escapeVCard(input.FamilyName) + ";" + escapeVCard(input.GivenName) + ";;;",
		"FN:" + escapeVCard(fn),
	}
	for _, email := range input.Emails {
		if strings.TrimSpace(email) != "" {
			lines = append(lines, "EMAIL;TYPE=INTERNET:"+escapeVCard(email))
		}
	}
	for _, phone := range input.Phones {
		if strings.TrimSpace(phone) != "" {
			lines = append(lines, "TEL:"+escapeVCard(phone))
		}
	}
	if strings.TrimSpace(input.Organization) != "" {
		lines = append(lines, "ORG:"+escapeVCard(input.Organization))
	}
	if strings.TrimSpace(input.Note) != "" {
		lines = append(lines, "NOTE:"+escapeVCard(input.Note))
	}
	lines = append(lines, "END:VCARD", "")
	return strings.Join(lines, "\r\n"), nil
}

func formatICalTime(s string) string {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format("20060102T150405Z")
	}
	return strings.NewReplacer("-", "", ":", "", ".", "").Replace(s)
}

func escapeICal(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", "", ";", `\;`, ",", `\,`)
	return replacer.Replace(s)
}

func escapeVCard(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", "", ";", `\;`, ",", `\,`)
	return replacer.Replace(s)
}

func normalizeLines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\r\n") + "\r\n"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func usage() map[string]any {
	return map[string]any{
		"usage": "icloud <auth|services|log|mail|calendar|contacts> ...",
		"examples": []string{
			"icloud auth check",
			"icloud services list",
			"icloud log status",
			"icloud mail folders list",
			"icloud mail messages list --folder INBOX --limit 10",
			`icloud mail batch move --input-json '{"folder":"INBOX","ids":["123"],"to_folder":"Archive"}'`,
		},
	}
}

func logFailure(code int, service, operation string, start time.Time, err error) {
	args := []any{
		"service", service,
		"operation", operation,
		"exit_code", code,
		"duration_ms", time.Since(start).Milliseconds(),
		"error_code", errorCode(err),
		"error_message", err.Error(),
	}
	if code == output.ExitUnexpected || code == output.ExitRemote {
		logging.Error("command_failure", args...)
		return
	}
	logging.Warn("command_failure", args...)
}

func errorCode(err error) string {
	if exitErr, ok := err.(*output.ExitError); ok {
		return exitErr.Err.Code
	}
	return "unexpected_error"
}
