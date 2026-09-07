package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net"
	netmail "net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/net/html/charset"

	"github.com/aaronfaby/icloud-cli/internal/config"
	"github.com/aaronfaby/icloud-cli/internal/logging"
	"github.com/aaronfaby/icloud-cli/internal/output"
)

const (
	DefaultIMAPHost      = "imap.mail.me.com:993"
	DefaultTrash         = "Trash"
	DefaultArchive       = "Archive"
	maxIMAPLineBytes     = 1 << 20
	maxIMAPResponseBytes = 64 << 20
	maxIMAPResponseLines = 100000
)

type IMAPClient struct {
	conn       net.Conn
	r          *bufio.Reader
	w          *bufio.Writer
	tag        int
	ctx        context.Context
	cancel     context.CancelFunc
	stopCancel func() bool
}

type imapResponse struct {
	Lines    []string
	Literals []string
}

func DialIMAP(ctx context.Context, cfg config.Config) (*IMAPClient, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	connected := false
	defer func() {
		if !connected {
			cancel()
		}
	}()
	logging.Info("imap_connect_start", "host", DefaultIMAPHost)
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	tlsDialer := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: "imap.mail.me.com", MinVersion: tls.VersionTLS12}}
	conn, err := tlsDialer.DialContext(ctx, "tcp", DefaultIMAPHost)
	if err != nil {
		logging.Error("imap_connect_failed", "host", DefaultIMAPHost, "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return nil, output.Remote("imap_connect_failed", "failed to connect to iCloud IMAP", err.Error())
	}
	c := &IMAPClient{conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn), ctx: ctx, cancel: cancel}
	c.stopCancel = context.AfterFunc(ctx, func() { _ = conn.Close() })
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	greeting, err := c.readIMAPLine()
	if err != nil {
		_ = conn.Close()
		logging.Error("imap_greeting_failed", "host", DefaultIMAPHost, "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return nil, output.Remote("imap_greeting_failed", "failed to read iCloud IMAP greeting", err.Error())
	}
	if !strings.HasPrefix(strings.ToUpper(greeting), "* OK ") && !strings.EqualFold(strings.TrimSpace(greeting), "* OK") {
		_ = c.Close()
		return nil, output.Remote("imap_greeting_failed", "unexpected iCloud IMAP greeting", nil)
	}
	if err := c.Login(ctx, cfg.AppleID, cfg.AppPassword); err != nil {
		_ = conn.Close()
		return nil, err
	}
	logging.Info("imap_connect_success", "host", DefaultIMAPHost, "duration_ms", time.Since(start).Milliseconds())
	connected = true
	return c, nil
}

func (c *IMAPClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	// Closing the transport needs no round trip and cannot hang on a stalled LOGOUT.
	if c.stopCancel != nil {
		c.stopCancel()
	}
	if c.cancel != nil {
		c.cancel()
	}
	logging.Info("imap_close")
	return c.conn.Close()
}

func (c *IMAPClient) Login(ctx context.Context, appleID, appPassword string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetDeadline(deadline); err != nil {
			return err
		}
	}
	start := time.Now()
	_, err := c.command("LOGIN %s %s", quote(appleID), quote(appPassword))
	if err != nil {
		logging.Warn("imap_login_failed", "duration_ms", time.Since(start).Milliseconds())
		return output.Auth("imap_login_failed", "iCloud IMAP login failed", err.Error())
	}
	logging.Info("imap_login_success", "duration_ms", time.Since(start).Milliseconds())
	return nil
}

func (c *IMAPClient) ListFolders() ([]Folder, error) {
	resp, err := c.command(`LIST "" "*"`)
	if err != nil {
		return nil, output.Remote("imap_list_failed", "failed to list mail folders", err.Error())
	}
	var folders []Folder
	literalIndex := 0
	for _, line := range resp.Lines {
		var literals []string
		if _, ok := literalSize(line); ok && literalIndex < len(resp.Literals) {
			literals = resp.Literals[literalIndex : literalIndex+1]
			literalIndex++
		}
		if !strings.HasPrefix(strings.ToUpper(line), "* LIST ") {
			continue
		}
		folder := parseFolder(line, literals...)
		if folder.Name == "" {
			return nil, output.Remote("imap_list_failed", "invalid IMAP LIST response", nil)
		}
		folders = append(folders, folder)
	}
	logging.Info("imap_folders_listed", "count", len(folders))
	return folders, nil
}

func (c *IMAPClient) AppendMessage(folder string, flags []string, date time.Time, msg []byte) error {
	if strings.TrimSpace(folder) == "" {
		return output.Validation("missing_folder", "folder is required", nil)
	}
	for _, flag := range flags {
		if err := validateIMAPFlag(flag); err != nil {
			return err
		}
	}
	flagsPart := ""
	if len(flags) > 0 {
		flagsPart = " (" + strings.Join(flags, " ") + ")"
	}
	datePart := ""
	if !date.IsZero() {
		datePart = " " + quote(date.Format("02-Jan-2006 15:04:05 -0700"))
	}
	msg = ensureCRLF(msg)
	_, err := c.commandLiteral("APPEND %s%s%s {%d}", msg, quoteMailbox(folder), flagsPart, datePart, len(msg))
	if err != nil {
		logging.Error("imap_append_failed", "folder", folder, "bytes", len(msg), "error", err.Error())
		return output.Remote("imap_append_failed", "failed to append mail message", err.Error())
	}
	logging.Info("imap_message_appended", "folder", folder, "bytes", len(msg), "flag_count", len(flags))
	return nil
}

func (c *IMAPClient) CreateFolder(name string) error {
	if strings.TrimSpace(name) == "" {
		return output.Validation("missing_folder_name", "folder name is required", nil)
	}
	_, err := c.command("CREATE %s", quoteMailbox(name))
	if err != nil {
		logging.Error("imap_create_folder_failed", "error", err.Error())
		return output.Remote("imap_create_folder_failed", "failed to create mail folder", err.Error())
	}
	logging.Info("imap_folder_created")
	return nil
}

func (c *IMAPClient) RenameFolder(folder, name string) error {
	if strings.TrimSpace(folder) == "" || strings.TrimSpace(name) == "" {
		return output.Validation("missing_folder_name", "source and destination folder names are required", nil)
	}
	_, err := c.command("RENAME %s %s", quoteMailbox(folder), quoteMailbox(name))
	if err != nil {
		logging.Error("imap_rename_folder_failed", "error", err.Error())
		return output.Remote("imap_rename_folder_failed", "failed to rename mail folder", err.Error())
	}
	logging.Info("imap_folder_renamed")
	return nil
}

func (c *IMAPClient) DeleteFolder(folder string) error {
	if strings.TrimSpace(folder) == "" {
		return output.Validation("missing_folder", "folder is required", nil)
	}
	_, err := c.command("DELETE %s", quoteMailbox(folder))
	if err != nil {
		logging.Error("imap_delete_folder_failed", "error", err.Error())
		return output.Remote("imap_delete_folder_failed", "failed to delete mail folder", err.Error())
	}
	logging.Info("imap_folder_deleted")
	return nil
}

func (c *IMAPClient) ListMessages(folder string, limit int) ([]MessageSummary, error) {
	return c.ListMessagesWithOptions(MessageListOptions{Folder: folder, Limit: limit})
}

func (c *IMAPClient) ListMessagesWithOptions(opts MessageListOptions) ([]MessageSummary, error) {
	folder := defaultFolder(opts.Folder)
	var ids []string
	var err error
	if !isASCII(opts.From) {
		from := strings.TrimSpace(opts.From)
		filters := opts
		filters.From = ""
		ids, err = c.search(folder, buildSearchCriteria(filters)+" FROM", &from)
	} else {
		ids, err = c.Search(folder, buildSearchCriteria(opts))
	}
	if err != nil {
		return nil, err
	}
	if opts.Limit > 0 && len(ids) > opts.Limit {
		ids = ids[len(ids)-opts.Limit:]
	}
	out := make([]MessageSummary, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		msg, err := c.FetchMessageWithOptions(folder, ids[i], FetchOptions{RawHeaders: opts.RawHeaders})
		if err != nil {
			return nil, err
		}
		out = append(out, msg.MessageSummary)
	}
	return out, nil
}

func (c *IMAPClient) Search(folder, query string) ([]string, error) {
	if err := validateIMAPLine(query); err != nil {
		return nil, err
	}
	criteria := strings.TrimSpace(query)
	if criteria == "" {
		criteria = "ALL"
	} else if !looksLikeIMAPCriteria(criteria) {
		if !isASCII(criteria) {
			return c.search(defaultFolder(folder), "TEXT", &criteria)
		}
		criteria = "TEXT " + quote(criteria)
	}
	if !isASCII(criteria) {
		return nil, output.Validation("unsupported_search_charset", "non-ASCII raw IMAP criteria are unsupported; use plain-text search or the --from list filter", nil)
	}
	return c.search(defaultFolder(folder), criteria, nil)
}

func (c *IMAPClient) search(folder, criteria string, literal *string) ([]string, error) {
	if err := validateIMAPLine(criteria); err != nil {
		return nil, err
	}
	if literal != nil {
		if err := validateIMAPLine(*literal); err != nil {
			return nil, err
		}
	}
	if err := c.selectFolder(folder); err != nil {
		return nil, err
	}
	var resp imapResponse
	var err error
	if literal != nil {
		resp, err = c.commandLiteral("UID SEARCH CHARSET UTF-8 %s {%d}", []byte(*literal), criteria, len(*literal))
	} else {
		resp, err = c.command("UID SEARCH %s", criteria)
	}
	if err != nil {
		logging.Error("imap_search_failed", "folder", folder, "error", err.Error())
		return nil, output.Remote("imap_search_failed", "failed to search mail", err.Error())
	}
	for _, line := range resp.Lines {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "*" && strings.EqualFold(fields[1], "SEARCH") {
			ids := parseSearch(line)
			logging.Info("imap_search_completed", "folder", folder, "count", len(ids))
			return ids, nil
		}
	}
	logging.Info("imap_search_completed", "folder", folder, "count", 0)
	return nil, nil
}

func (c *IMAPClient) FetchMessage(folder, id string, includeRaw bool) (Message, error) {
	return c.FetchMessageWithOptions(folder, id, FetchOptions{IncludeRaw: includeRaw})
}

func (c *IMAPClient) FetchMessageWithOptions(folder, id string, opts FetchOptions) (Message, error) {
	id, err := validateUID(id)
	if err != nil {
		return Message{}, err
	}
	if strings.TrimSpace(folder) == "" {
		folder = "INBOX"
	}
	if err := c.selectFolder(folder); err != nil {
		return Message{}, err
	}
	item := "BODY.PEEK[HEADER]"
	if wantsFullMessage(opts) {
		item = "BODY.PEEK[]"
	}
	resp, err := c.command("UID FETCH %s (UID FLAGS INTERNALDATE RFC822.SIZE %s)", id, item)
	if err != nil {
		logging.Error("imap_fetch_failed", "folder", folder, "id", id, "include_raw", opts.IncludeRaw, "error", err.Error())
		return Message{}, output.Remote("imap_fetch_failed", "failed to fetch mail message", err.Error())
	}
	resp, found := requestedFetch(resp, id)
	if !found {
		return Message{}, output.Remote("message_not_found", "mail message was not found", map[string]string{"id": id})
	}
	msg := parseFetch(folder, id, resp, opts)
	raw := ""
	if wantsFullMessage(opts) && len(resp.Literals) > 0 {
		raw = resp.Literals[len(resp.Literals)-1]
	}
	if opts.IncludeRaw && raw != "" {
		msg.Raw = raw
		parsed, err := netmail.ReadMessage(strings.NewReader(raw))
		if err == nil {
			body, _ := io.ReadAll(parsed.Body)
			msg.Headers = map[string][]string(parsed.Header)
			msg.Body = string(body)
			applyHeaders(&msg.MessageSummary, parsed.Header, opts.RawHeaders)
		}
	}
	if raw != "" && (opts.BodyMode != "" || opts.IncludeAttachments) {
		content, err := parseMessageContent(raw)
		if err != nil {
			return Message{}, output.Remote("message_parse_failed", "failed to parse mail message", err.Error())
		}
		switch opts.BodyMode {
		case "text":
			msg.Body = content.Text
		case "html":
			msg.HTML = content.HTML
		}
		if opts.IncludeAttachments {
			msg.Attachments = content.Attachments
		}
	}
	logging.Info("imap_message_fetched", "folder", folder, "id", id, "include_raw", opts.IncludeRaw, "body_mode", opts.BodyMode, "include_attachments", opts.IncludeAttachments, "raw_bytes", len(msg.Raw), "body_bytes", len(msg.Body))
	return msg, nil
}

func (c *IMAPClient) FetchAttachment(folder, id string, attachmentID string) (Attachment, error) {
	if strings.TrimSpace(attachmentID) == "" {
		return Attachment{}, output.Validation("missing_attachment_id", "attachment id is required", nil)
	}
	msg, err := c.FetchMessageWithOptions(folder, id, FetchOptions{IncludeRaw: true})
	if err != nil {
		return Attachment{}, err
	}
	attachment, ok, err := attachmentWithContent(msg.Raw, attachmentID)
	if err != nil {
		return Attachment{}, output.Remote("message_parse_failed", "failed to parse mail message", err.Error())
	}
	if !ok {
		return Attachment{}, output.Validation("attachment_not_found", "attachment id was not found", map[string]string{"attachment": attachmentID})
	}
	return attachment, nil
}

func wantsFullMessage(opts FetchOptions) bool {
	return opts.IncludeRaw || opts.BodyMode != "" || opts.IncludeAttachments
}

func (c *IMAPClient) Move(folder, id, toFolder string) error {
	id, err := validateUID(id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(toFolder) == "" {
		return output.Validation("missing_destination_folder", "destination folder is required", nil)
	}
	if err := c.selectFolder(defaultFolder(folder)); err != nil {
		return err
	}
	_, err = c.command("UID MOVE %s %s", id, quoteMailbox(toFolder))
	if err == nil {
		logging.Info("imap_message_moved", "folder", folder, "id", id)
		return nil
	}
	if _, copyErr := c.command("UID COPY %s %s", id, quoteMailbox(toFolder)); copyErr != nil {
		logging.Error("imap_move_failed", "folder", folder, "id", id, "error", err.Error())
		return output.Remote("imap_move_failed", "failed to move mail message", err.Error())
	}
	if _, storeErr := c.command(`UID STORE %s +FLAGS.SILENT (\Deleted)`, id); storeErr != nil {
		logging.Error("imap_move_cleanup_failed", "folder", folder, "id", id, "error", storeErr.Error())
		return output.Remote("imap_move_cleanup_failed", "message copied but source could not be marked deleted", storeErr.Error())
	}
	// Prefer UID EXPUNGE so only the target message is removed. Never fall back to
	// mailbox-wide EXPUNGE, which would permanently delete unrelated \Deleted mail.
	if _, expungeErr := c.command("UID EXPUNGE %s", id); expungeErr != nil {
		logging.Error("imap_move_expunge_failed", "folder", folder, "id", id, "error", expungeErr.Error())
		return output.Remote("imap_move_cleanup_failed", "message copied and marked deleted, but source could not be expunged (UIDPLUS required)", expungeErr.Error())
	}
	logging.Warn("imap_message_moved_with_copy_delete_fallback", "folder", folder, "id", id)
	return nil
}

func (c *IMAPClient) Copy(folder, id, toFolder string) error {
	id, err := validateUID(id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(toFolder) == "" {
		return output.Validation("missing_destination_folder", "destination folder is required", nil)
	}
	if err := c.selectFolder(defaultFolder(folder)); err != nil {
		return err
	}
	if _, err := c.command("UID COPY %s %s", id, quoteMailbox(toFolder)); err != nil {
		logging.Error("imap_copy_failed", "folder", folder, "id", id, "error", err.Error())
		return output.Remote("imap_copy_failed", "failed to copy mail message", err.Error())
	}
	logging.Info("imap_message_copied", "folder", folder, "id", id)
	return nil
}

func (c *IMAPClient) Delete(folder, id, trashFolder string, permanent bool, dryRun bool) (MutationResult, error) {
	rawID := strings.TrimSpace(id)
	id, err := validateUID(id)
	if err != nil {
		return MutationResult{ID: rawID, OK: false, Error: err.Error()}, err
	}
	if dryRun {
		mode := "move_to_trash"
		if permanent {
			mode = "permanent_delete"
		}
		logging.Info("imap_delete_dry_run", "folder", folder, "id", id, "mode", mode)
		return MutationResult{ID: id, OK: true, Warning: "dry_run:" + mode}, nil
	}
	if !permanent {
		if shouldDiscoverTrashFolder(trashFolder) {
			folders, err := c.ListFolders()
			if err == nil {
				trashFolder = chooseTrashFolder(folders, trashFolder)
			}
		}
		if strings.TrimSpace(trashFolder) == "" {
			trashFolder = DefaultTrash
		}
		return MutationResult{ID: id, OK: true}, c.Move(folder, id, trashFolder)
	}
	if err := c.selectFolder(defaultFolder(folder)); err != nil {
		return MutationResult{ID: id, OK: false, Error: err.Error()}, err
	}
	if _, err := c.command(`UID STORE %s +FLAGS.SILENT (\Deleted)`, id); err != nil {
		logging.Error("imap_delete_failed", "folder", folder, "id", id, "error", err.Error())
		return MutationResult{ID: id, OK: false, Error: err.Error()}, output.Remote("imap_delete_failed", "failed to mark mail message deleted", err.Error())
	}
	if _, err := c.command("UID EXPUNGE %s", id); err == nil {
		logging.Info("imap_message_permanently_deleted", "folder", folder, "id", id)
		return MutationResult{ID: id, OK: true}, nil
	}
	// Do not fall back to mailbox-wide EXPUNGE: that permanently removes every
	// message already marked \Deleted in the selected folder.
	logging.Error("imap_uid_expunge_unsupported", "folder", folder, "id", id)
	err = output.Remote("imap_expunge_failed", "permanent delete requires UID EXPUNGE (UIDPLUS); message was marked deleted but not expunged to avoid removing unrelated mail", map[string]string{"id": id, "folder": folder})
	return MutationResult{ID: id, OK: false, Error: err.Error(), Warning: "message marked \\Deleted; mailbox-wide EXPUNGE was not used"}, err
}

func (c *IMAPClient) Archive(folder, id, archiveFolder string) error {
	if strings.TrimSpace(archiveFolder) == "" {
		archiveFolder = DefaultArchive
	}
	return c.Move(folder, id, archiveFolder)
}

func (c *IMAPClient) SetFlag(folder, id, flag string, enable bool) error {
	if err := validateIMAPFlag(flag); err != nil {
		return err
	}
	id, err := validateUID(id)
	if err != nil {
		return err
	}
	if err := c.selectFolder(defaultFolder(folder)); err != nil {
		return err
	}
	op := "+FLAGS.SILENT"
	if !enable {
		op = "-FLAGS.SILENT"
	}
	if _, err := c.command("UID STORE %s %s (%s)", id, op, flag); err != nil {
		logging.Error("imap_flag_failed", "folder", folder, "id", id, "flag", flag, "enable", enable, "error", err.Error())
		return output.Remote("imap_flag_failed", "failed to update mail flags", err.Error())
	}
	logging.Info("imap_flag_updated", "folder", folder, "id", id, "flag", flag, "enable", enable)
	return nil
}

func (c *IMAPClient) selectFolder(folder string) error {
	_, err := c.command("SELECT %s", quoteMailbox(folder))
	if err != nil {
		logging.Error("imap_select_failed", "folder", folder, "error", err.Error())
		return output.Remote("imap_select_failed", "failed to select mail folder", err.Error())
	}
	logging.Info("imap_folder_selected", "folder", folder)
	return nil
}

func (c *IMAPClient) command(format string, args ...any) (imapResponse, error) {
	line := fmt.Sprintf(format, args...)
	if err := validateIMAPLine(line); err != nil {
		return imapResponse{}, err
	}
	if c.ctx != nil {
		if err := c.ctx.Err(); err != nil {
			return imapResponse{}, err
		}
	}
	c.tag++
	tag := fmt.Sprintf("A%04d", c.tag)
	if _, err := fmt.Fprintf(c.w, "%s %s\r\n", tag, line); err != nil {
		return imapResponse{}, err
	}
	if err := c.w.Flush(); err != nil {
		return imapResponse{}, err
	}
	resp, err := c.readUntilTag(tag)
	if err != nil {
		logging.Error("imap_command_failed", "command", imapCommandName(line), "error", err.Error())
		return resp, err
	}
	if len(resp.Lines) == 0 {
		logging.Error("imap_command_empty_response", "command", imapCommandName(line))
		return resp, fmt.Errorf("empty IMAP response")
	}
	last := resp.Lines[len(resp.Lines)-1]
	if fields := strings.Fields(last); len(fields) < 2 || fields[0] != tag || !strings.EqualFold(fields[1], "OK") {
		logging.Warn("imap_command_rejected", "command", imapCommandName(line), "line_count", len(resp.Lines), "literal_count", len(resp.Literals))
		return resp, fmt.Errorf("IMAP %s rejected by server", imapCommandName(line))
	}
	logging.Info("imap_command_ok", "command", imapCommandName(line), "line_count", len(resp.Lines), "literal_count", len(resp.Literals))
	return resp, nil
}

func (c *IMAPClient) commandLiteral(format string, literal []byte, args ...any) (imapResponse, error) {
	line := fmt.Sprintf(format, args...)
	if err := validateIMAPLine(line); err != nil {
		return imapResponse{}, err
	}
	if c.ctx != nil {
		if err := c.ctx.Err(); err != nil {
			return imapResponse{}, err
		}
	}
	c.tag++
	tag := fmt.Sprintf("A%04d", c.tag)
	if _, err := fmt.Fprintf(c.w, "%s %s\r\n", tag, line); err != nil {
		return imapResponse{}, err
	}
	if err := c.w.Flush(); err != nil {
		return imapResponse{}, err
	}
	pendingBytes, pendingLines := 0, 0
	for {
		continuation, err := c.readIMAPLine()
		if err != nil {
			return imapResponse{}, err
		}
		pendingBytes += len(continuation)
		pendingLines++
		if pendingBytes > maxIMAPResponseBytes || pendingLines > maxIMAPResponseLines {
			_ = c.Close()
			return imapResponse{}, fmt.Errorf("IMAP continuation response exceeds size limit")
		}
		if strings.HasPrefix(continuation, "+") {
			break
		}
		if strings.HasPrefix(continuation, "* ") && !strings.HasPrefix(strings.ToUpper(continuation), "* BYE") {
			continue
		}
		return imapResponse{}, fmt.Errorf("IMAP literal rejected by server")
	}
	if _, err := c.w.Write(literal); err != nil {
		return imapResponse{}, err
	}
	if _, err := c.w.WriteString("\r\n"); err != nil {
		return imapResponse{}, err
	}
	if err := c.w.Flush(); err != nil {
		return imapResponse{}, err
	}
	resp, err := c.readUntilTag(tag)
	if err != nil {
		logging.Error("imap_literal_command_failed", "command", imapCommandName(line), "error", err.Error())
		return resp, err
	}
	if len(resp.Lines) == 0 {
		logging.Error("imap_literal_command_empty_response", "command", imapCommandName(line))
		return resp, fmt.Errorf("empty IMAP response")
	}
	last := resp.Lines[len(resp.Lines)-1]
	if fields := strings.Fields(last); len(fields) < 2 || fields[0] != tag || !strings.EqualFold(fields[1], "OK") {
		logging.Warn("imap_literal_command_rejected", "command", imapCommandName(line), "line_count", len(resp.Lines), "literal_count", len(resp.Literals))
		return resp, fmt.Errorf("IMAP %s rejected by server", imapCommandName(line))
	}
	logging.Info("imap_literal_command_ok", "command", imapCommandName(line), "line_count", len(resp.Lines), "literal_count", len(resp.Literals), "literal_bytes", len(literal))
	return resp, nil
}

func imapCommandName(line string) string {
	fields := strings.Fields(logging.SanitizedIMAPCommand(line))
	if len(fields) == 0 {
		return ""
	}
	if strings.EqualFold(fields[0], "UID") && len(fields) > 1 {
		return "UID " + strings.ToUpper(fields[1])
	}
	return strings.ToUpper(fields[0])
}

func (c *IMAPClient) readIMAPLine() (string, error) {
	var line strings.Builder
	for {
		fragment, err := c.r.ReadSlice('\n')
		if line.Len()+len(fragment) > maxIMAPLineBytes {
			_ = c.Close()
			return "", fmt.Errorf("IMAP response line exceeds 1 MiB")
		}
		line.Write(fragment)
		if err != bufio.ErrBufferFull {
			return line.String(), err
		}
	}
}

func (c *IMAPClient) readUntilTag(tag string) (imapResponse, error) {
	resp := imapResponse{}
	total := 0
	for {
		line, err := c.readIMAPLine()
		if err != nil {
			return resp, err
		}
		total += len(line)
		if total > maxIMAPResponseBytes || len(resp.Lines) >= maxIMAPResponseLines {
			_ = c.Close()
			return resp, fmt.Errorf("IMAP response exceeds size limit")
		}
		line = strings.TrimRight(line, "\r\n")
		resp.Lines = append(resp.Lines, line)
		n, literal := literalSize(line)
		if !literal && strings.HasSuffix(line, "}") && strings.Contains(line, "{") {
			_ = c.Close()
			return resp, fmt.Errorf("invalid IMAP literal size")
		}
		if literal {
			if n > maxMessageBytes || n > maxIMAPResponseBytes-total {
				_ = c.Close()
				return resp, fmt.Errorf("IMAP literal or response exceeds size limit")
			}
			total += n
			var data strings.Builder
			data.Grow(n)
			if _, err := io.CopyN(&data, c.r, int64(n)); err != nil {
				return resp, err
			}
			// Keep literals only in Literals — never in Lines. Message bodies can
			// contain substrings like "FLAGS (" that would confuse protocol parsers.
			resp.Literals = append(resp.Literals, data.String())
		}
		if strings.HasPrefix(line, tag+" ") {
			return resp, nil
		}
	}
}

func validateIMAPLine(line string) error {
	if strings.ContainsAny(line, "\r\n\x00") || !utf8.ValidString(line) {
		return output.Validation("invalid_imap_command", "IMAP command values cannot contain NUL, line breaks, or invalid UTF-8", nil)
	}
	return nil
}

func quoteMailbox(name string) string {
	// Leave invalid input visible to the shared command validator; never encode it away.
	if validateIMAPLine(name) != nil {
		return quote(name)
	}
	return quote(encodeModifiedUTF7(name))
}

func encodeModifiedUTF7(name string) string {
	var out strings.Builder
	var nonASCII []rune
	flush := func() {
		if len(nonASCII) == 0 {
			return
		}
		var data []byte
		for _, word := range utf16.Encode(nonASCII) {
			data = append(data, byte(word>>8), byte(word))
		}
		out.WriteByte('&')
		out.WriteString(strings.ReplaceAll(base64.RawStdEncoding.EncodeToString(data), "/", ","))
		out.WriteByte('-')
		nonASCII = nonASCII[:0]
	}
	for _, r := range name {
		if r >= 0x20 && r <= 0x7e {
			flush()
			if r == '&' {
				out.WriteString("&-")
			} else {
				out.WriteRune(r)
			}
		} else {
			nonASCII = append(nonASCII, r)
		}
	}
	flush()
	return out.String()
}

func validateIMAPFlag(flag string) error {
	atom := strings.TrimPrefix(flag, `\`)
	if atom == "" {
		return output.Validation("invalid_flag", "IMAP flag must be a single atom", nil)
	}
	for _, r := range atom {
		if r <= 0x20 || r >= 0x7f || strings.ContainsRune(`(){%*"\]`, r) {
			return output.Validation("invalid_flag", "IMAP flag must be a single atom", nil)
		}
	}
	return nil
}

func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func literalSize(line string) (int, bool) {
	start := strings.LastIndex(line, "{")
	if start < 0 || !strings.HasSuffix(line, "}") {
		return 0, false
	}
	n, err := strconv.Atoi(line[start+1 : len(line)-1])
	return n, err == nil && n >= 0
}

func parseFolder(line string, literals ...string) Folder {
	f := Folder{Flags: parseParen(line)}
	end := strings.IndexByte(line, ')')
	if end < 0 {
		return f
	}
	delimiter, rest, ok := imapString(line[end+1:], &literals)
	if !ok {
		return f
	}
	if token := strings.Fields(line[end+1:]); len(token) > 0 && !strings.EqualFold(token[0], "NIL") {
		f.Delimiter = delimiter
	}
	name, _, ok := imapString(rest, &literals)
	if ok {
		f.Name = decodeModifiedUTF7(name)
	}
	return f
}

// imapString reads a quoted string, literal, or atom without losing its boundary.
func imapString(s string, literals *[]string) (string, string, bool) {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return "", "", false
	}
	if s[0] == '"' {
		var value strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] == '"' {
				return value.String(), s[i+1:], true
			}
			if s[i] == '\\' {
				i++
				if i == len(s) {
					break
				}
			}
			value.WriteByte(s[i])
		}
		return "", "", false
	}
	if _, ok := literalSize(s); ok {
		if len(*literals) == 0 {
			return "", "", false
		}
		value := (*literals)[0]
		*literals = (*literals)[1:]
		return value, "", true
	}
	end := strings.IndexAny(s, " \t")
	if end < 0 {
		return s, "", true
	}
	return s[:end], s[end:], true
}

func parseSearch(line string) []string {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return nil
	}
	parts = parts[2:]
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if id, err := validateUID(p); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// IMAP attributes are name/value pairs. Parenthesized flags and quoted strings
// are single values, so their contents cannot masquerade as UID or size metadata.
func imapToken(s string) (string, string, bool) {
	s = strings.TrimLeft(s, " \t\r\n")
	if s == "" || s[0] == ')' {
		return "", s, false
	}
	paren, bracket := 0, 0
	bodySection := strings.HasPrefix(strings.ToUpper(s), "BODY[")
	quoted, escaped := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if quoted {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				quoted = false
			}
			continue
		}
		switch ch {
		case '"':
			quoted = true
		case '[':
			if bodySection {
				bracket++
			}
		case ']':
			if bodySection {
				bracket--
			}
		case '(':
			paren++
		case ')':
			if paren == 0 && bracket == 0 {
				return s[:i], s[i:], i > 0
			}
			paren--
		case ' ', '\t', '\r', '\n':
			if paren == 0 && bracket == 0 {
				return s[:i], s[i:], true
			}
		}
		if paren < 0 || bracket < 0 {
			return "", s, false
		}
	}
	return s, "", !quoted && paren == 0 && bracket == 0
}

func fetchAttributes(lines []string) map[string]string {
	attrs := map[string]string{}
	protocol := strings.Join(lines, " ")
	start := strings.IndexByte(protocol, '(')
	if start < 0 {
		return attrs
	}
	protocol = protocol[start+1:]
	for {
		name, rest, ok := imapToken(protocol)
		if !ok {
			break
		}
		value, rest, ok := imapToken(rest)
		if !ok {
			break
		}
		attrs[strings.ToUpper(name)] = value
		protocol = rest
	}
	return attrs
}

func fetchAttributeGroups(lines []string) []map[string]string {
	var groups []map[string]string
	var current []string
	for _, line := range lines {
		if strings.HasPrefix(line, "* ") && len(current) > 0 {
			groups = append(groups, fetchAttributes(current))
			current = nil
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		groups = append(groups, fetchAttributes(current))
	}
	return groups
}

// requestedFetch groups each untagged FETCH with its continuations and literals.
// Unsolicited responses for other UIDs must never supply this message's metadata.
func requestedFetch(resp imapResponse, id string) (imapResponse, bool) {
	var selected, current imapResponse
	found := false
	finish := func() {
		attrs := fetchAttributes(current.Lines)
		if attrs["UID"] == id {
			_, full := attrs["BODY[]"]
			_, header := attrs["BODY[HEADER]"]
			found = found || full || header
			selected.Lines = append(selected.Lines, current.Lines...)
			selected.Literals = append(selected.Literals, current.Literals...)
		}
		current = imapResponse{}
	}
	literalIndex := 0
	for _, line := range resp.Lines {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "*" || len(fields) > 1 && (strings.EqualFold(fields[1], "OK") || strings.EqualFold(fields[1], "NO") || strings.EqualFold(fields[1], "BAD")) {
			finish()
		}
		if len(fields) >= 3 && fields[0] == "*" && strings.EqualFold(fields[2], "FETCH") || len(current.Lines) > 0 {
			current.Lines = append(current.Lines, line)
		}
		if _, ok := literalSize(line); ok && literalIndex < len(resp.Literals) {
			if len(current.Lines) > 0 {
				current.Literals = append(current.Literals, resp.Literals[literalIndex])
			}
			literalIndex++
		}
	}
	finish()
	return selected, found
}

func parseFetch(folder, id string, resp imapResponse, opts FetchOptions) Message {
	resp, found := requestedFetch(resp, id)
	msg := Message{MessageSummary: MessageSummary{Folder: folder}}
	if !found {
		return msg
	}
	msg.ID = id
	for _, attrs := range fetchAttributeGroups(resp.Lines) {
		if date, exists := attrs["INTERNALDATE"]; exists {
			literals := []string(nil)
			msg.InternalDate, _, _ = imapString(date, &literals)
		}
		if size, exists := attrs["RFC822.SIZE"]; exists {
			msg.Size, _ = strconv.Atoi(size)
		}
		if flags, exists := attrs["FLAGS"]; exists {
			msg.Flags = parseParen(flags)
		}
	}
	if len(resp.Literals) > 0 {
		raw := resp.Literals[len(resp.Literals)-1]
		if opts.IncludeRaw {
			msg.Raw = raw
		}
		if parsed, err := netmail.ReadMessage(bytes.NewBufferString(raw)); err == nil {
			applyHeaders(&msg.MessageSummary, parsed.Header, opts.RawHeaders)
		}
	}
	return msg
}

func applyHeaders(summary *MessageSummary, header netmail.Header, includeRaw bool) {
	rawSubject := header.Get("Subject")
	rawFrom := header.Get("From")
	rawTo := header.Get("To")
	rawCC := header.Get("Cc")
	rawReplyTo := header.Get("Reply-To")
	rawDate := header.Get("Date")
	summary.Subject = decodeHeaderValue(rawSubject)
	summary.From = strings.Join(splitAddressList(rawFrom), ", ")
	summary.Date = rawDate
	summary.References = strings.TrimSpace(header.Get("References"))
	summary.MessageID = header.Get("Message-Id")
	if summary.MessageID == "" {
		summary.MessageID = header.Get("Message-ID")
	}
	if rawTo != "" {
		summary.To = splitAddressList(rawTo)
	}
	if rawCC != "" {
		summary.CC = splitAddressList(rawCC)
	}
	if rawReplyTo != "" {
		summary.ReplyTo = splitAddressList(rawReplyTo)
	}
	if includeRaw {
		summary.RawSubject = rawSubject
		summary.RawFrom = rawFrom
		summary.RawTo = rawTo
		summary.RawDate = rawDate
	}
}

func parseParen(line string) []string {
	start := strings.Index(line, "(")
	end := strings.Index(line, ")")
	if start < 0 || end <= start {
		return nil
	}
	return strings.Fields(line[start+1 : end])
}

func decodeModifiedUTF7(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '&' {
			out.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i+1:], '-')
		if end < 0 {
			out.WriteByte(s[i])
			i++
			continue
		}
		encoded := s[i+1 : i+1+end]
		if encoded == "" {
			out.WriteByte('&')
		} else if decoded, ok := decodeModifiedUTF7Segment(encoded); ok {
			out.WriteString(decoded)
		} else {
			out.WriteString(s[i : i+end+2])
		}
		i += end + 2
	}
	return out.String()
}

func decodeModifiedUTF7Segment(encoded string) (string, bool) {
	encoded = strings.ReplaceAll(encoded, ",", "/")
	if rem := len(encoded) % 4; rem != 0 {
		encoded += strings.Repeat("=", 4-rem)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data)%2 != 0 {
		return "", false
	}
	words := make([]uint16, 0, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		words = append(words, uint16(data[i])<<8|uint16(data[i+1]))
	}
	return string(utf16.Decode(words)), true
}

func splitAddressList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parser := netmail.AddressParser{WordDecoder: &mime.WordDecoder{CharsetReader: charset.NewReaderLabel}}
	addrs, err := parser.ParseList(raw)
	if err != nil {
		return []string{raw}
	}
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, formatAddress(addr))
	}
	return out
}

func formatAddress(addr *netmail.Address) string {
	address := (&netmail.Address{Address: addr.Address}).String()
	if strings.TrimSpace(addr.Name) == "" {
		return strings.TrimSuffix(strings.TrimPrefix(address, "<"), ">")
	}
	// JSON summaries keep decoded names; quote the phrase without RFC 2047 encoding.
	name := strings.NewReplacer("\r", " ", "\n", " ").Replace(addr.Name)
	return quote(name) + " " + address
}

func decodeHeaderValue(raw string) string {
	if raw == "" {
		return ""
	}
	decoded, err := (&mime.WordDecoder{CharsetReader: charset.NewReaderLabel}).DecodeHeader(raw)
	if err != nil {
		return raw
	}
	return decoded
}

func buildSearchCriteria(opts MessageListOptions) string {
	var criteria []string
	if opts.Unread {
		criteria = append(criteria, "UNSEEN")
	}
	if opts.Flagged {
		criteria = append(criteria, "FLAGGED")
	}
	if strings.TrimSpace(opts.Since) != "" {
		criteria = append(criteria, "SINCE "+strings.TrimSpace(opts.Since))
	}
	if strings.TrimSpace(opts.From) != "" {
		criteria = append(criteria, "FROM "+quote(strings.TrimSpace(opts.From)))
	}
	if len(criteria) == 0 {
		return "ALL"
	}
	return strings.Join(criteria, " ")
}

func looksLikeIMAPCriteria(query string) bool {
	upper := strings.ToUpper(strings.TrimSpace(query))
	keywords := []string{"ALL", "UNSEEN", "SEEN", "FLAGGED", "UNFLAGGED", "FROM ", "TO ", "SUBJECT ", "TEXT ", "SINCE ", "BEFORE ", "UID "}
	for _, kw := range keywords {
		if upper == strings.TrimSpace(kw) || strings.HasPrefix(upper, strings.TrimSpace(kw)+" ") {
			return true
		}
	}
	return false
}

func defaultFolder(folder string) string {
	if strings.TrimSpace(folder) == "" {
		return "INBOX"
	}
	return folder
}

// validateUID accepts a single decimal IMAP UID. Multi-token or non-numeric
// values must be rejected before they are interpolated into IMAP commands.
func validateUID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", output.Validation("missing_message_id", "message id is required", nil)
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return "", output.Validation("invalid_message_id", "message id must be a decimal IMAP UID", map[string]string{"id": id})
		}
	}
	value, err := strconv.ParseUint(id, 10, 32)
	if err != nil || value == 0 {
		return "", output.Validation("invalid_message_id", "message id must be a nonzero 32-bit IMAP UID", nil)
	}
	return strconv.FormatUint(value, 10), nil
}

func ensureCRLF(msg []byte) []byte {
	if bytes.HasSuffix(msg, []byte("\r\n")) {
		return msg
	}
	out := append([]byte{}, msg...)
	out = append(out, '\r', '\n')
	return out
}

func shouldDiscoverTrashFolder(folder string) bool {
	folder = strings.TrimSpace(folder)
	return folder == "" || strings.EqualFold(folder, DefaultTrash)
}

func chooseTrashFolder(folders []Folder, requested string) string {
	if !shouldDiscoverTrashFolder(requested) {
		return requested
	}
	for _, folder := range folders {
		for _, flag := range folder.Flags {
			if strings.EqualFold(flag, `\Trash`) {
				return folder.Name
			}
		}
	}
	for _, folder := range folders {
		name := strings.ToLower(folder.Name)
		if name == "trash" || name == "deleted messages" || strings.Contains(name, "trash") {
			return folder.Name
		}
	}
	if strings.TrimSpace(requested) != "" {
		return requested
	}
	return DefaultTrash
}

func chooseSentFolder(folders []Folder, requested string) string {
	if strings.TrimSpace(requested) != "" {
		return requested
	}
	for _, folder := range folders {
		for _, flag := range folder.Flags {
			if strings.EqualFold(flag, `\Sent`) {
				return folder.Name
			}
		}
	}
	for _, folder := range folders {
		name := strings.ToLower(folder.Name)
		if name == "sent" || name == "sent messages" || strings.Contains(name, "sent") {
			return folder.Name
		}
	}
	return "Sent"
}

func chooseDraftFolder(folders []Folder, requested string) string {
	if strings.TrimSpace(requested) != "" {
		return requested
	}
	for _, folder := range folders {
		for _, flag := range folder.Flags {
			if strings.EqualFold(flag, `\Drafts`) || strings.EqualFold(flag, `\Draft`) {
				return folder.Name
			}
		}
	}
	for _, folder := range folders {
		name := strings.ToLower(folder.Name)
		if name == "drafts" || name == "draft" || strings.Contains(name, "draft") {
			return folder.Name
		}
	}
	return "Drafts"
}
