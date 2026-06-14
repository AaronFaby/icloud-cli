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
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/aaronfaby/icloud-cli/internal/config"
	"github.com/aaronfaby/icloud-cli/internal/logging"
	"github.com/aaronfaby/icloud-cli/internal/output"
)

const (
	DefaultIMAPHost = "imap.mail.me.com:993"
	DefaultTrash    = "Trash"
	DefaultArchive  = "Archive"
)

type IMAPClient struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
	tag  int
}

type imapResponse struct {
	Lines    []string
	Literals []string
}

func DialIMAP(ctx context.Context, cfg config.Config) (*IMAPClient, error) {
	start := time.Now()
	logging.Info("imap_connect_start", "host", DefaultIMAPHost)
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", DefaultIMAPHost, &tls.Config{ServerName: "imap.mail.me.com", MinVersion: tls.VersionTLS12})
	if err != nil {
		logging.Error("imap_connect_failed", "host", DefaultIMAPHost, "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return nil, output.Remote("imap_connect_failed", "failed to connect to iCloud IMAP", err.Error())
	}
	c := &IMAPClient{conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn)}
	if _, err := c.r.ReadString('\n'); err != nil {
		_ = conn.Close()
		logging.Error("imap_greeting_failed", "host", DefaultIMAPHost, "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return nil, output.Remote("imap_greeting_failed", "failed to read iCloud IMAP greeting", err.Error())
	}
	if err := c.Login(ctx, cfg.AppleID, cfg.AppPassword); err != nil {
		_ = conn.Close()
		return nil, err
	}
	logging.Info("imap_connect_success", "host", DefaultIMAPHost, "duration_ms", time.Since(start).Milliseconds())
	return c, nil
}

func (c *IMAPClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	_, _ = c.command("LOGOUT")
	logging.Info("imap_close")
	return c.conn.Close()
}

func (c *IMAPClient) Login(_ context.Context, appleID, appPassword string) error {
	start := time.Now()
	_, err := c.command("LOGIN %s %s", quote(appleID), quote(appPassword))
	if err != nil {
		logging.Warn("imap_login_failed", "duration_ms", time.Since(start).Milliseconds())
		return output.Auth("imap_login_failed", "iCloud IMAP login failed", redactIMAPError(err.Error()))
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
	for _, line := range resp.Lines {
		if !strings.HasPrefix(line, "* LIST ") {
			continue
		}
		folders = append(folders, parseFolder(line))
	}
	logging.Info("imap_folders_listed", "count", len(folders))
	return folders, nil
}

func (c *IMAPClient) AppendMessage(folder string, flags []string, date time.Time, msg []byte) error {
	if strings.TrimSpace(folder) == "" {
		return output.Validation("missing_folder", "folder is required", nil)
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
	_, err := c.commandLiteral("APPEND %s%s%s {%d}", msg, quote(folder), flagsPart, datePart, len(msg))
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
	_, err := c.command("CREATE %s", quote(name))
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
	_, err := c.command("RENAME %s %s", quote(folder), quote(name))
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
	_, err := c.command("DELETE %s", quote(folder))
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
	ids, err := c.Search(folder, buildSearchCriteria(opts))
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
	if strings.TrimSpace(folder) == "" {
		folder = "INBOX"
	}
	if err := c.selectFolder(folder); err != nil {
		return nil, err
	}
	criteria := strings.TrimSpace(query)
	if criteria == "" {
		criteria = "ALL"
	} else if !looksLikeIMAPCriteria(criteria) {
		criteria = "TEXT " + quote(criteria)
	}
	resp, err := c.command("UID SEARCH %s", criteria)
	if err != nil {
		logging.Error("imap_search_failed", "folder", folder, "error", err.Error())
		return nil, output.Remote("imap_search_failed", "failed to search mail", err.Error())
	}
	for _, line := range resp.Lines {
		if strings.HasPrefix(line, "* SEARCH") {
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
	if strings.TrimSpace(id) == "" {
		return Message{}, output.Validation("missing_message_id", "message id is required", nil)
	}
	if strings.TrimSpace(folder) == "" {
		folder = "INBOX"
	}
	if err := c.selectFolder(folder); err != nil {
		return Message{}, err
	}
	item := "BODY.PEEK[HEADER]"
	if opts.IncludeRaw {
		item = "BODY.PEEK[]"
	}
	resp, err := c.command("UID FETCH %s (UID FLAGS INTERNALDATE RFC822.SIZE %s)", id, item)
	if err != nil {
		logging.Error("imap_fetch_failed", "folder", folder, "id", id, "include_raw", opts.IncludeRaw, "error", err.Error())
		return Message{}, output.Remote("imap_fetch_failed", "failed to fetch mail message", err.Error())
	}
	msg := parseFetch(folder, id, resp, opts)
	if msg.ID == "" {
		msg.ID = id
	}
	if opts.IncludeRaw && msg.Raw != "" {
		parsed, err := netmail.ReadMessage(strings.NewReader(msg.Raw))
		if err == nil {
			body, _ := io.ReadAll(parsed.Body)
			msg.Headers = map[string][]string(parsed.Header)
			msg.Body = string(body)
			applyHeaders(&msg.MessageSummary, parsed.Header, opts.RawHeaders)
		}
	}
	logging.Info("imap_message_fetched", "folder", folder, "id", id, "include_raw", opts.IncludeRaw, "raw_bytes", len(msg.Raw), "body_bytes", len(msg.Body))
	return msg, nil
}

func (c *IMAPClient) Move(folder, id, toFolder string) error {
	if strings.TrimSpace(toFolder) == "" {
		return output.Validation("missing_destination_folder", "destination folder is required", nil)
	}
	if err := c.selectFolder(defaultFolder(folder)); err != nil {
		return err
	}
	_, err := c.command("UID MOVE %s %s", id, quote(toFolder))
	if err == nil {
		logging.Info("imap_message_moved", "folder", folder, "id", id)
		return nil
	}
	if _, copyErr := c.command("UID COPY %s %s", id, quote(toFolder)); copyErr != nil {
		logging.Error("imap_move_failed", "folder", folder, "id", id, "error", err.Error())
		return output.Remote("imap_move_failed", "failed to move mail message", err.Error())
	}
	if _, storeErr := c.command(`UID STORE %s +FLAGS.SILENT (\Deleted)`, id); storeErr != nil {
		logging.Error("imap_move_cleanup_failed", "folder", folder, "id", id, "error", storeErr.Error())
		return output.Remote("imap_move_cleanup_failed", "message copied but source could not be marked deleted", storeErr.Error())
	}
	_, _ = c.command("EXPUNGE")
	logging.Warn("imap_message_moved_with_copy_delete_fallback", "folder", folder, "id", id)
	return nil
}

func (c *IMAPClient) Copy(folder, id, toFolder string) error {
	if strings.TrimSpace(toFolder) == "" {
		return output.Validation("missing_destination_folder", "destination folder is required", nil)
	}
	if err := c.selectFolder(defaultFolder(folder)); err != nil {
		return err
	}
	if _, err := c.command("UID COPY %s %s", id, quote(toFolder)); err != nil {
		logging.Error("imap_copy_failed", "folder", folder, "id", id, "error", err.Error())
		return output.Remote("imap_copy_failed", "failed to copy mail message", err.Error())
	}
	logging.Info("imap_message_copied", "folder", folder, "id", id)
	return nil
}

func (c *IMAPClient) Delete(folder, id, trashFolder string, permanent bool, dryRun bool) (MutationResult, error) {
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
	if _, err := c.command("EXPUNGE"); err != nil {
		logging.Error("imap_expunge_failed", "folder", folder, "id", id, "error", err.Error())
		return MutationResult{ID: id, OK: false, Error: err.Error()}, output.Remote("imap_expunge_failed", "failed to permanently delete mail message", err.Error())
	}
	logging.Warn("imap_expunge_fallback_used", "folder", folder, "id", id)
	return MutationResult{ID: id, OK: true, Warning: "server did not accept UID EXPUNGE; mailbox EXPUNGE fallback was used"}, nil
}

func (c *IMAPClient) Archive(folder, id, archiveFolder string) error {
	if strings.TrimSpace(archiveFolder) == "" {
		archiveFolder = DefaultArchive
	}
	return c.Move(folder, id, archiveFolder)
}

func (c *IMAPClient) SetFlag(folder, id, flag string, enable bool) error {
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
	_, err := c.command("SELECT %s", quote(folder))
	if err != nil {
		logging.Error("imap_select_failed", "folder", folder, "error", err.Error())
		return output.Remote("imap_select_failed", "failed to select mail folder", err.Error())
	}
	logging.Info("imap_folder_selected", "folder", folder)
	return nil
}

func (c *IMAPClient) command(format string, args ...any) (imapResponse, error) {
	c.tag++
	tag := fmt.Sprintf("A%04d", c.tag)
	line := fmt.Sprintf(format, args...)
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
	if !strings.HasPrefix(last, tag+" OK") {
		logging.Warn("imap_command_rejected", "command", imapCommandName(line), "line_count", len(resp.Lines), "literal_count", len(resp.Literals))
		return resp, fmt.Errorf("%s", last)
	}
	logging.Info("imap_command_ok", "command", imapCommandName(line), "line_count", len(resp.Lines), "literal_count", len(resp.Literals))
	return resp, nil
}

func (c *IMAPClient) commandLiteral(format string, literal []byte, args ...any) (imapResponse, error) {
	c.tag++
	tag := fmt.Sprintf("A%04d", c.tag)
	line := fmt.Sprintf(format, args...)
	if _, err := fmt.Fprintf(c.w, "%s %s\r\n", tag, line); err != nil {
		return imapResponse{}, err
	}
	if err := c.w.Flush(); err != nil {
		return imapResponse{}, err
	}
	continuation, err := c.r.ReadString('\n')
	if err != nil {
		logging.Error("imap_literal_continuation_failed", "command", imapCommandName(line), "error", err.Error())
		return imapResponse{}, err
	}
	continuation = strings.TrimRight(continuation, "\r\n")
	if !strings.HasPrefix(continuation, "+") {
		logging.Warn("imap_literal_rejected", "command", imapCommandName(line))
		return imapResponse{Lines: []string{continuation}}, fmt.Errorf("%s", continuation)
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
	if !strings.HasPrefix(last, tag+" OK") {
		logging.Warn("imap_literal_command_rejected", "command", imapCommandName(line), "line_count", len(resp.Lines), "literal_count", len(resp.Literals))
		return resp, fmt.Errorf("%s", last)
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

func (c *IMAPClient) readUntilTag(tag string) (imapResponse, error) {
	resp := imapResponse{}
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return resp, err
		}
		line = strings.TrimRight(line, "\r\n")
		resp.Lines = append(resp.Lines, line)
		if n, ok := literalSize(line); ok {
			data := make([]byte, n)
			if _, err := io.ReadFull(c.r, data); err != nil {
				return resp, err
			}
			literal := string(data)
			resp.Literals = append(resp.Literals, literal)
			resp.Lines = append(resp.Lines, literal)
		}
		if strings.HasPrefix(line, tag+" ") {
			return resp, nil
		}
	}
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
	return n, err == nil
}

func parseFolder(line string) Folder {
	flags := parseParen(line)
	quoted := quotedValues(line)
	f := Folder{Flags: flags}
	if len(quoted) >= 2 {
		f.Delimiter = quoted[len(quoted)-2]
		f.Name = decodeModifiedUTF7(quoted[len(quoted)-1])
	} else if len(quoted) == 1 {
		f.Name = decodeModifiedUTF7(quoted[0])
	}
	return f
}

func parseSearch(line string) []string {
	parts := strings.Fields(strings.TrimPrefix(line, "* SEARCH"))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func parseFetch(folder, fallbackID string, resp imapResponse, opts FetchOptions) Message {
	msg := Message{MessageSummary: MessageSummary{ID: fallbackID, Folder: folder}}
	for _, line := range resp.Lines {
		if strings.Contains(line, "FETCH") {
			msg.ID = firstSubmatch(`UID ([0-9]+)`, line, msg.ID)
			msg.InternalDate = firstSubmatch(`INTERNALDATE "([^"]+)"`, line, msg.InternalDate)
			if size := firstSubmatch(`RFC822\.SIZE ([0-9]+)`, line, ""); size != "" {
				msg.Size, _ = strconv.Atoi(size)
			}
		}
		if flags := parseFlags(line); len(flags) > 0 {
			msg.Flags = flags
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
	summary.From = decodeHeaderValue(rawFrom)
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

func parseFlags(line string) []string {
	idx := strings.Index(line, "FLAGS (")
	if idx < 0 {
		return nil
	}
	start := idx + len("FLAGS (")
	end := strings.Index(line[start:], ")")
	if end < 0 {
		return nil
	}
	return strings.Fields(line[start : start+end])
}

func quotedValues(line string) []string {
	var values []string
	inQuote := false
	escaped := false
	var b strings.Builder
	for _, r := range line {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && inQuote:
			escaped = true
		case r == '"':
			if inQuote {
				values = append(values, b.String())
				b.Reset()
			}
			inQuote = !inQuote
		case inQuote:
			b.WriteRune(r)
		}
	}
	return values
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

func firstSubmatch(pattern, input, fallback string) string {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(input)
	if len(m) < 2 {
		return fallback
	}
	return m[1]
}

func splitAddressList(raw string) []string {
	decoded := decodeHeaderValue(raw)
	addrs, err := netmail.ParseAddressList(decoded)
	if err != nil {
		return []string{decoded}
	}
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, formatAddress(addr))
	}
	return out
}

func formatAddress(addr *netmail.Address) string {
	if strings.TrimSpace(addr.Name) == "" {
		return addr.Address
	}
	return strings.TrimSpace(addr.Name) + " <" + addr.Address + ">"
}

func decodeHeaderValue(raw string) string {
	if raw == "" {
		return ""
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(raw)
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
		if upper == strings.TrimSpace(kw) || strings.HasPrefix(upper, kw) {
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

func redactIMAPError(s string) string {
	re := regexp.MustCompile(`(?i)LOGIN\s+"[^"]+"\s+"[^"]+"`)
	return re.ReplaceAllString(s, `LOGIN "****" "****"`)
}
