package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/textproto"
	"sort"
	"strings"
	"time"

	"github.com/aaronfaby/icloud-cli/internal/config"
	"github.com/aaronfaby/icloud-cli/internal/logging"
	"github.com/aaronfaby/icloud-cli/internal/output"
)

const DefaultSMTPHost = "smtp.mail.me.com:587"

func Send(cfg config.Config, req SendRequest) (map[string]any, error) {
	if strings.TrimSpace(req.From) == "" {
		req.From = cfg.AppleID
	}
	if strings.TrimSpace(req.From) == "" {
		return nil, output.Validation("missing_from", "from address is required", nil)
	}
	if len(req.To) == 0 && len(req.CC) == 0 && len(req.BCC) == 0 {
		return nil, output.Validation("missing_recipients", "at least one recipient is required", nil)
	}
	msg, err := buildMessage(req)
	if err != nil {
		return nil, err
	}
	recipients := append(append([]string{}, req.To...), req.CC...)
	recipients = append(recipients, req.BCC...)
	logging.Info("smtp_send_start", "host", DefaultSMTPHost, "to_count", len(req.To), "cc_count", len(req.CC), "bcc_count", len(req.BCC), "message_bytes", len(msg))
	if err := sendMailTLS(DefaultSMTPHost, cfg.AppleID, cfg.AppPassword, req.From, recipients, msg); err != nil {
		logging.Error("smtp_send_failed", "host", DefaultSMTPHost, "recipient_count", len(recipients), "error", err.Error())
		return nil, output.Remote("smtp_send_failed", "failed to send iCloud mail", err.Error())
	}
	logging.Info("smtp_send_success", "host", DefaultSMTPHost, "recipient_count", len(recipients), "message_bytes", len(msg))
	return map[string]any{
		"from":      req.From,
		"to":        req.To,
		"cc":        req.CC,
		"bcc_count": len(req.BCC),
		"subject":   req.Subject,
		"sent_copy": appendSentCopy(cfg, msg),
	}, nil
}

func appendSentCopy(cfg config.Config, msg []byte) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	client, err := DialIMAP(ctx, cfg)
	if err != nil {
		logging.Warn("sent_copy_imap_failed")
		return map[string]any{"ok": false, "error": err.Error()}
	}
	defer client.Close()

	folders, err := client.ListFolders()
	if err != nil {
		logging.Warn("sent_copy_folder_list_failed", "error", err.Error())
		return map[string]any{"ok": false, "error": err.Error()}
	}
	folder := chooseSentFolder(folders, "")
	if err := client.AppendMessage(folder, []string{`\Seen`}, time.Now(), msg); err != nil {
		logging.Warn("sent_copy_append_failed", "folder", folder, "error", err.Error())
		return map[string]any{"ok": false, "folder": folder, "error": err.Error()}
	}
	logging.Info("sent_copy_append_success", "folder", folder, "bytes", len(msg))
	return map[string]any{"ok": true, "folder": folder}
}

func AppendDraft(client *IMAPClient, req SendRequest) (map[string]any, error) {
	msg, err := buildMessage(req)
	if err != nil {
		return nil, err
	}
	folders, err := client.ListFolders()
	if err != nil {
		logging.Warn("draft_folder_list_failed", "error", err.Error())
		return nil, err
	}
	folder := chooseDraftFolder(folders, "")
	if err := client.AppendMessage(folder, []string{`\Draft`}, time.Now(), msg); err != nil {
		logging.Warn("draft_append_failed", "folder", folder, "error", err.Error())
		return nil, err
	}
	logging.Info("draft_append_success", "folder", folder, "bytes", len(msg))
	return map[string]any{"ok": true, "folder": folder}, nil
}

func sendMailTLS(addr string, username string, password string, from string, to []string, msg []byte) error {
	start := time.Now()
	logging.Info("smtp_connect_start", "host", addr)
	conn, err := net.DialTimeout("tcp", addr, 20*time.Second)
	if err != nil {
		logging.Error("smtp_connect_failed", "host", addr, "duration_ms", time.Since(start).Milliseconds(), "error", err.Error())
		return err
	}
	defer conn.Close()

	tc := textproto.NewConn(conn)
	if _, _, err := tc.ReadResponse(220); err != nil {
		logging.Error("smtp_greeting_failed", "host", addr, "error", err.Error())
		return err
	}
	if err := smtpCommand(tc, 250, "EHLO localhost"); err != nil {
		return err
	}
	if err := smtpCommand(tc, 220, "STARTTLS"); err != nil {
		return err
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: "smtp.mail.me.com", MinVersion: tls.VersionTLS12})
	if err := tlsConn.Handshake(); err != nil {
		logging.Error("smtp_tls_failed", "host", addr, "error", err.Error())
		return err
	}
	logging.Info("smtp_tls_success", "host", addr)
	tc = textproto.NewConn(tlsConn)
	defer tc.Close()
	if err := smtpCommand(tc, 250, "EHLO localhost"); err != nil {
		return err
	}
	plain := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password))
	if err := smtpCommand(tc, 235, "AUTH PLAIN "+plain); err != nil {
		logging.Warn("smtp_auth_failed", "host", addr)
		return err
	}
	logging.Info("smtp_auth_success", "host", addr)
	if err := smtpCommand(tc, 250, "MAIL FROM:<"+from+">"); err != nil {
		return err
	}
	for _, addr := range to {
		if err := smtpCommand(tc, 250, "RCPT TO:<"+addr+">"); err != nil {
			return err
		}
	}
	logging.Info("smtp_recipients_accepted", "count", len(to))
	if err := smtpCommand(tc, 354, "DATA"); err != nil {
		return err
	}
	w := tc.DotWriter()
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	_, _, err = tc.ReadResponse(250)
	if err != nil {
		logging.Error("smtp_data_failed", "message_bytes", len(msg), "error", err.Error())
		return err
	}
	_ = smtpCommand(tc, 221, "QUIT")
	logging.Info("smtp_send_completed", "host", addr, "duration_ms", time.Since(start).Milliseconds(), "recipient_count", len(to), "message_bytes", len(msg))
	return nil
}

func smtpCommand(c *textproto.Conn, expect int, line string) error {
	id, err := c.Cmd("%s", line)
	if err != nil {
		return err
	}
	c.StartResponse(id)
	defer c.EndResponse(id)
	_, _, err = c.ReadResponse(expect)
	if err != nil {
		logging.Warn("smtp_command_failed", "command", smtpCommandName(line), "expect", expect, "error", err.Error())
	} else {
		logging.Info("smtp_command_ok", "command", smtpCommandName(line), "expect", expect)
	}
	return err
}

func smtpCommandName(line string) string {
	sanitized := logging.SanitizedSMTPCommand(line)
	upper := strings.ToUpper(sanitized)
	switch {
	case strings.HasPrefix(upper, "AUTH "):
		return "AUTH"
	case strings.HasPrefix(upper, "MAIL FROM:"):
		return "MAIL FROM"
	case strings.HasPrefix(upper, "RCPT TO:"):
		return "RCPT TO"
	}
	fields := strings.Fields(sanitized)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}

func buildMessage(req SendRequest) ([]byte, error) {
	var b bytes.Buffer
	headers := map[string]string{
		"From":         req.From,
		"To":           strings.Join(req.To, ", "),
		"Subject":      req.Subject,
		"Date":         time.Now().Format(time.RFC1123Z),
		"MIME-Version": "1.0",
	}
	if len(req.CC) > 0 {
		headers["Cc"] = strings.Join(req.CC, ", ")
	}
	for k, v := range req.Headers {
		if isProtectedHeader(k) {
			continue
		}
		headers[k] = v
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s: %s\r\n", k, encodeHeader(headers[k]))
	}
	if req.HTML != "" {
		writer := multipart.NewWriter(&b)
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", writer.Boundary())
		writePart(writer, "text/plain; charset=utf-8", req.Text)
		writePart(writer, "text/html; charset=utf-8", req.HTML)
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	fmt.Fprintf(&b, "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := qp.Write([]byte(req.Text)); err != nil {
		_ = qp.Close()
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writePart(writer *multipart.Writer, contentType string, body string) {
	part, err := writer.CreatePart(map[string][]string{
		"Content-Type":              {contentType},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return
	}
	qp := quotedprintable.NewWriter(part)
	_, _ = qp.Write([]byte(body))
	_ = qp.Close()
}

func encodeHeader(value string) string {
	if value == "" || isASCII(value) {
		return value
	}
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(value)) + "?="
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func isProtectedHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "from", "to", "cc", "bcc", "subject", "date", "mime-version", "content-type", "content-transfer-encoding":
		return true
	default:
		return false
	}
}
