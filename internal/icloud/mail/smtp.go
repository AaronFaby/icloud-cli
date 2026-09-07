package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
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
		logging.Error("smtp_send_failed", "host", DefaultSMTPHost, "recipient_count", len(recipients))
		var typed *output.ExitError
		if errors.As(err, &typed) {
			return nil, err
		}
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
		logging.Warn("sent_copy_folder_list_failed")
		return map[string]any{"ok": false, "error": err.Error()}
	}
	folder := chooseSentFolder(folders, "")
	if err := client.AppendMessage(folder, []string{`\Seen`}, time.Now(), msg); err != nil {
		logging.Warn("sent_copy_append_failed", "folder", folder)
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
		logging.Warn("draft_folder_list_failed")
		return nil, err
	}
	folder := chooseDraftFolder(folders, "")
	if err := client.AppendMessage(folder, []string{`\Draft`}, time.Now(), msg); err != nil {
		logging.Warn("draft_append_failed", "folder", folder)
		return nil, err
	}
	logging.Info("draft_append_success", "folder", folder, "bytes", len(msg))
	return map[string]any{"ok": true, "folder": folder}, nil
}

func sendMailTLS(addr string, username string, password string, from string, to []string, msg []byte) error {
	return sendMailTLSContext(context.Background(), addr, username, password, from, to, msg)
}

func sendMailTLSContext(ctx context.Context, addr, username, password, from string, to []string, msg []byte) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	defer func() {
		if resultErr != nil && ctx.Err() != nil {
			resultErr = ctx.Err()
		}
	}()
	start := time.Now()
	logging.Info("smtp_connect_start", "host", addr)
	conn, err := (&net.Dialer{Timeout: 20 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		logging.Error("smtp_connect_failed", "host", addr, "duration_ms", time.Since(start).Milliseconds())
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	tc := textproto.NewConn(conn)
	if _, _, err := tc.ReadResponse(220); err != nil {
		logging.Error("smtp_greeting_failed", "host", addr)
		return err
	}
	if err := smtpCommand(tc, 250, "EHLO localhost"); err != nil {
		return err
	}
	if err := smtpCommand(tc, 220, "STARTTLS"); err != nil {
		return err
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: "smtp.mail.me.com", MinVersion: tls.VersionTLS12})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		logging.Error("smtp_tls_failed", "host", addr)
		return err
	}
	logging.Info("smtp_tls_success", "host", addr)
	tc = textproto.NewConn(tlsConn)
	defer tc.Close()
	if err := smtpCommand(tc, 250, "EHLO localhost"); err != nil {
		return err
	}
	if err := smtpAuthenticate(tc, username, password); err != nil {
		logging.Warn("smtp_auth_failed", "host", addr)
		return err
	}
	logging.Info("smtp_auth_success", "host", addr)
	fromAddr, err := envelopeAddress(from)
	if err != nil {
		return err
	}
	if err := smtpCommand(tc, 250, "MAIL FROM:<"+fromAddr+">"); err != nil {
		return err
	}
	for _, recipient := range to {
		if err := smtpRecipient(tc, recipient); err != nil {
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
	if err := finishSMTPSend(tc, conn, deadline); err != nil {
		logging.Error("smtp_data_failed", "message_bytes", len(msg))
		return err
	}
	logging.Info("smtp_send_completed", "host", addr, "duration_ms", time.Since(start).Milliseconds(), "recipient_count", len(to), "message_bytes", len(msg))
	return nil
}

func smtpRecipient(tc *textproto.Conn, recipient string) error {
	rcpt, err := envelopeAddress(recipient)
	if err != nil {
		return err
	}
	// Like net/smtp.Rcpt, accept 25x (including 251 forwarding success).
	return smtpCommand(tc, 25, "RCPT TO:<"+rcpt+">")
}

func smtpAuthenticate(tc *textproto.Conn, username, password string) error {
	plain := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password))
	err := smtpCommand(tc, 235, "AUTH PLAIN "+plain)
	var reply *textproto.Error
	if errors.As(err, &reply) {
		// Authentication replies can echo credentials; expose only the status.
		return output.Auth("smtp_auth_failed", "iCloud SMTP authentication failed", map[string]any{"status": reply.Code})
	}
	return err
}

func finishSMTPSend(tc *textproto.Conn, conn net.Conn, deadline time.Time) error {
	if _, _, err := tc.ReadResponse(250); err != nil {
		return err
	}
	// The server accepted DATA. Cleanup failure must not report a failed send:
	// retrying at this point could send a duplicate message.
	cleanupDeadline := time.Now().Add(2 * time.Second)
	if deadline.Before(cleanupDeadline) {
		cleanupDeadline = deadline
	}
	_ = conn.SetDeadline(cleanupDeadline)
	_ = smtpCommand(tc, 221, "QUIT")
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
		logging.Warn("smtp_command_failed", "command", smtpCommandName(line), "expect", expect)
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
	if len(req.Text)+len(req.HTML) > maxMessageBytes {
		return nil, output.Validation("message_limit", "message body exceeds 32 MiB", nil)
	}
	attachments, _, err := resolveAttachments(req.Attachments)
	if err != nil {
		return nil, err
	}

	from, err := addressHeader([]string{req.From})
	if err != nil {
		return nil, err
	}
	to, err := addressHeader(req.To)
	if err != nil {
		return nil, err
	}
	cc, err := addressHeader(req.CC)
	if err != nil {
		return nil, err
	}
	if _, err := addressHeader(req.BCC); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	headers := map[string]string{
		"From":         from,
		"To":           to,
		"Subject":      req.Subject,
		"Date":         time.Now().Format(time.RFC1123Z),
		"MIME-Version": "1.0",
	}
	if len(req.CC) > 0 {
		headers["Cc"] = cc
	}
	for k, v := range req.Headers {
		if !validHeaderName(k) {
			return nil, output.Validation("invalid_header_name", "custom header name must contain printable ASCII except colon", nil)
		}
		if isProtectedHeader(k) {
			continue
		}
		switch strings.ToLower(k) {
		case "reply-to", "sender", "resent-from", "resent-to", "resent-cc", "resent-sender":
			addrs, err := netmail.ParseAddressList(v)
			if err != nil {
				return nil, output.Validation("invalid_address_header", "invalid address header", nil)
			}
			values := make([]string, len(addrs))
			for i, addr := range addrs {
				values[i] = addr.String()
			}
			v, err = addressHeader(values)
			if err != nil {
				return nil, err
			}
		}
		headers[k] = v
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		line := k + ": " + encodeHeader(headers[k])
		if len(line) > 998 {
			return nil, output.Validation("header_too_long", "encoded mail header exceeds the 998-octet line limit", nil)
		}
		b.WriteString(line + "\r\n")
	}
	if len(attachments) > 0 {
		writer := multipart.NewWriter(&b)
		fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", writer.Boundary())
		if req.HTML != "" {
			var body bytes.Buffer
			alt := multipart.NewWriter(&body)
			if err := writePart(alt, "text/plain; charset=utf-8", req.Text); err != nil {
				return nil, err
			}
			if err := writePart(alt, "text/html; charset=utf-8", req.HTML); err != nil {
				return nil, err
			}
			if err := alt.Close(); err != nil {
				return nil, err
			}
			part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": alt.Boundary()})}})
			if err != nil {
				return nil, err
			}
			part.Write(body.Bytes())
		} else if err := writePart(writer, "text/plain; charset=utf-8", req.Text); err != nil {
			return nil, err
		}
		for _, a := range attachments {
			part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {a.ContentType}, "Content-Disposition": {mime.FormatMediaType("attachment", map[string]string{"filename": a.Filename})}, "Content-Transfer-Encoding": {"base64"}})
			if err != nil {
				return nil, err
			}
			encoded := a.ContentBase64
			for len(encoded) > 0 {
				n := 76
				if len(encoded) < n {
					n = len(encoded)
				}
				fmt.Fprint(part, encoded[:n], "\r\n")
				encoded = encoded[n:]
			}
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		if b.Len() > maxMessageBytes {
			return nil, output.Validation("message_limit", "encoded message exceeds 32 MiB", nil)
		}
		return b.Bytes(), nil
	}
	if req.HTML != "" {
		writer := multipart.NewWriter(&b)
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", writer.Boundary())
		if err := writePart(writer, "text/plain; charset=utf-8", req.Text); err != nil {
			return nil, err
		}
		if err := writePart(writer, "text/html; charset=utf-8", req.HTML); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		if b.Len() > maxMessageBytes {
			return nil, output.Validation("message_limit", "encoded message exceeds 32 MiB", nil)
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
	if b.Len() > maxMessageBytes {
		return nil, output.Validation("message_limit", "encoded message exceeds 32 MiB", nil)
	}
	return b.Bytes(), nil
}

func writePart(writer *multipart.Writer, contentType string, body string) error {
	part, err := writer.CreatePart(map[string][]string{
		"Content-Type":              {contentType},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}
	qp := quotedprintable.NewWriter(part)
	if _, err := qp.Write([]byte(body)); err != nil {
		return err
	}
	return qp.Close()
}

func encodeHeader(value string) string {
	value = sanitizeHeaderValue(value)
	if value == "" || isASCII(value) {
		return value
	}
	return mime.BEncoding.Encode("UTF-8", value)
}

// sanitizeHeaderValue strips CR/LF so agent- or user-supplied header values
// cannot inject additional SMTP/MIME headers.
func sanitizeHeaderValue(value string) string {
	if value == "" {
		return value
	}
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

// envelopeAddress extracts a bare email address for SMTP MAIL FROM / RCPT TO.
// Display-name forms such as `Name <user@example.com>` are accepted for headers
// but must not be placed inside angle brackets in the SMTP envelope.
func envelopeAddress(raw string) (string, error) {
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", fmt.Errorf("invalid email address")
	}
	addr, err := netmail.ParseAddress(strings.TrimSpace(raw))
	if err != nil || addr == nil || addr.Address == "" || !isASCII(addr.Address) {
		return "", fmt.Errorf("invalid email address or unsupported non-ASCII mailbox")
	}
	addr.Name = ""
	// Address.String preserves quoting of unusual local parts in the envelope.
	return strings.TrimSuffix(strings.TrimPrefix(addr.String(), "<"), ">"), nil
}

func addressHeader(values []string) (string, error) {
	out := make([]string, 0, len(values))
	for _, raw := range values {
		if _, err := envelopeAddress(raw); err != nil {
			return "", output.Validation("invalid_address", err.Error(), nil)
		}
		addr, _ := netmail.ParseAddress(strings.TrimSpace(raw))
		value := addr.String()
		if addr.Name == "" {
			value = strings.TrimSuffix(strings.TrimPrefix(value, "<"), ">")
		}
		out = append(out, value)
	}
	return strings.Join(out, ", "), nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c < 33 || c > 126 || c == ':' {
			return false
		}
	}
	return true
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
	case "from", "to", "cc", "bcc", "subject", "date", "mime-version", "content-type", "content-transfer-encoding", "resent-bcc":
		return true
	default:
		return false
	}
}
