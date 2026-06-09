package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	netmail "net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aaronfaby/icloud-cli/internal/config"
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
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", DefaultIMAPHost, &tls.Config{ServerName: "imap.mail.me.com", MinVersion: tls.VersionTLS12})
	if err != nil {
		return nil, output.Remote("imap_connect_failed", "failed to connect to iCloud IMAP", err.Error())
	}
	c := &IMAPClient{conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn)}
	if _, err := c.r.ReadString('\n'); err != nil {
		_ = conn.Close()
		return nil, output.Remote("imap_greeting_failed", "failed to read iCloud IMAP greeting", err.Error())
	}
	if err := c.Login(ctx, cfg.AppleID, cfg.AppPassword); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *IMAPClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	_, _ = c.command("LOGOUT")
	return c.conn.Close()
}

func (c *IMAPClient) Login(_ context.Context, appleID, appPassword string) error {
	_, err := c.command("LOGIN %s %s", quote(appleID), quote(appPassword))
	if err != nil {
		return output.Auth("imap_login_failed", "iCloud IMAP login failed", redactIMAPError(err.Error()))
	}
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
		return output.Remote("imap_append_failed", "failed to append mail message", err.Error())
	}
	return nil
}

func (c *IMAPClient) CreateFolder(name string) error {
	if strings.TrimSpace(name) == "" {
		return output.Validation("missing_folder_name", "folder name is required", nil)
	}
	_, err := c.command("CREATE %s", quote(name))
	if err != nil {
		return output.Remote("imap_create_folder_failed", "failed to create mail folder", err.Error())
	}
	return nil
}

func (c *IMAPClient) RenameFolder(folder, name string) error {
	if strings.TrimSpace(folder) == "" || strings.TrimSpace(name) == "" {
		return output.Validation("missing_folder_name", "source and destination folder names are required", nil)
	}
	_, err := c.command("RENAME %s %s", quote(folder), quote(name))
	if err != nil {
		return output.Remote("imap_rename_folder_failed", "failed to rename mail folder", err.Error())
	}
	return nil
}

func (c *IMAPClient) DeleteFolder(folder string) error {
	if strings.TrimSpace(folder) == "" {
		return output.Validation("missing_folder", "folder is required", nil)
	}
	_, err := c.command("DELETE %s", quote(folder))
	if err != nil {
		return output.Remote("imap_delete_folder_failed", "failed to delete mail folder", err.Error())
	}
	return nil
}

func (c *IMAPClient) ListMessages(folder string, limit int) ([]MessageSummary, error) {
	ids, err := c.Search(folder, "ALL")
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(ids) > limit {
		ids = ids[len(ids)-limit:]
	}
	out := make([]MessageSummary, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		msg, err := c.FetchMessage(folder, ids[i], false)
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
		return nil, output.Remote("imap_search_failed", "failed to search mail", err.Error())
	}
	for _, line := range resp.Lines {
		if strings.HasPrefix(line, "* SEARCH") {
			return parseSearch(line), nil
		}
	}
	return nil, nil
}

func (c *IMAPClient) FetchMessage(folder, id string, includeRaw bool) (Message, error) {
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
	if includeRaw {
		item = "RFC822"
	}
	resp, err := c.command("UID FETCH %s (UID FLAGS INTERNALDATE RFC822.SIZE %s)", id, item)
	if err != nil {
		return Message{}, output.Remote("imap_fetch_failed", "failed to fetch mail message", err.Error())
	}
	msg := parseFetch(folder, id, resp, includeRaw)
	if msg.ID == "" {
		msg.ID = id
	}
	if includeRaw && msg.Raw != "" {
		parsed, err := netmail.ReadMessage(strings.NewReader(msg.Raw))
		if err == nil {
			body, _ := io.ReadAll(parsed.Body)
			msg.Headers = map[string][]string(parsed.Header)
			msg.Body = string(body)
			applyHeaders(&msg.MessageSummary, parsed.Header)
		}
	}
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
		return nil
	}
	if _, copyErr := c.command("UID COPY %s %s", id, quote(toFolder)); copyErr != nil {
		return output.Remote("imap_move_failed", "failed to move mail message", err.Error())
	}
	if _, storeErr := c.command(`UID STORE %s +FLAGS.SILENT (\Deleted)`, id); storeErr != nil {
		return output.Remote("imap_move_cleanup_failed", "message copied but source could not be marked deleted", storeErr.Error())
	}
	_, _ = c.command("EXPUNGE")
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
		return output.Remote("imap_copy_failed", "failed to copy mail message", err.Error())
	}
	return nil
}

func (c *IMAPClient) Delete(folder, id, trashFolder string, permanent bool, dryRun bool) (MutationResult, error) {
	if dryRun {
		mode := "move_to_trash"
		if permanent {
			mode = "permanent_delete"
		}
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
		return MutationResult{ID: id, OK: false, Error: err.Error()}, output.Remote("imap_delete_failed", "failed to mark mail message deleted", err.Error())
	}
	if _, err := c.command("UID EXPUNGE %s", id); err == nil {
		return MutationResult{ID: id, OK: true}, nil
	}
	if _, err := c.command("EXPUNGE"); err != nil {
		return MutationResult{ID: id, OK: false, Error: err.Error()}, output.Remote("imap_expunge_failed", "failed to permanently delete mail message", err.Error())
	}
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
		return output.Remote("imap_flag_failed", "failed to update mail flags", err.Error())
	}
	return nil
}

func (c *IMAPClient) selectFolder(folder string) error {
	_, err := c.command("SELECT %s", quote(folder))
	if err != nil {
		return output.Remote("imap_select_failed", "failed to select mail folder", err.Error())
	}
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
		return resp, err
	}
	if len(resp.Lines) == 0 {
		return resp, fmt.Errorf("empty IMAP response")
	}
	last := resp.Lines[len(resp.Lines)-1]
	if !strings.HasPrefix(last, tag+" OK") {
		return resp, fmt.Errorf("%s", last)
	}
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
		return imapResponse{}, err
	}
	continuation = strings.TrimRight(continuation, "\r\n")
	if !strings.HasPrefix(continuation, "+") {
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
		return resp, err
	}
	if len(resp.Lines) == 0 {
		return resp, fmt.Errorf("empty IMAP response")
	}
	last := resp.Lines[len(resp.Lines)-1]
	if !strings.HasPrefix(last, tag+" OK") {
		return resp, fmt.Errorf("%s", last)
	}
	return resp, nil
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
		f.Name = quoted[len(quoted)-1]
	} else if len(quoted) == 1 {
		f.Name = quoted[0]
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

func parseFetch(folder, fallbackID string, resp imapResponse, includeRaw bool) Message {
	msg := Message{MessageSummary: MessageSummary{ID: fallbackID, Folder: folder}}
	for _, line := range resp.Lines {
		if strings.Contains(line, "FETCH") {
			msg.ID = firstSubmatch(`UID ([0-9]+)`, line, msg.ID)
			msg.InternalDate = firstSubmatch(`INTERNALDATE "([^"]+)"`, line, msg.InternalDate)
			if size := firstSubmatch(`RFC822\.SIZE ([0-9]+)`, line, ""); size != "" {
				msg.Size, _ = strconv.Atoi(size)
			}
			msg.Flags = parseFlags(line)
		}
	}
	if len(resp.Literals) > 0 {
		raw := resp.Literals[len(resp.Literals)-1]
		if includeRaw {
			msg.Raw = raw
		}
		if parsed, err := netmail.ReadMessage(bytes.NewBufferString(raw)); err == nil {
			applyHeaders(&msg.MessageSummary, parsed.Header)
		}
	}
	return msg
}

func applyHeaders(summary *MessageSummary, header netmail.Header) {
	summary.Subject = header.Get("Subject")
	summary.From = header.Get("From")
	summary.Date = header.Get("Date")
	summary.MessageID = header.Get("Message-Id")
	if summary.MessageID == "" {
		summary.MessageID = header.Get("Message-ID")
	}
	if to := header.Get("To"); to != "" {
		summary.To = splitAddressList(to)
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

func firstSubmatch(pattern, input, fallback string) string {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(input)
	if len(m) < 2 {
		return fallback
	}
	return m[1]
}

func splitAddressList(raw string) []string {
	addrs, err := netmail.ParseAddressList(raw)
	if err != nil {
		return []string{raw}
	}
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, addr.String())
	}
	return out
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

func redactIMAPError(s string) string {
	re := regexp.MustCompile(`(?i)LOGIN\s+"[^"]+"\s+"[^"]+"`)
	return re.ReplaceAllString(s, `LOGIN "****" "****"`)
}
