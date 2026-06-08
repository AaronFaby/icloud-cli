package mail

import (
	"bytes"
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
	if err := sendMailTLS(DefaultSMTPHost, cfg.AppleID, cfg.AppPassword, req.From, recipients, msg); err != nil {
		return nil, output.Remote("smtp_send_failed", "failed to send iCloud mail", err.Error())
	}
	return map[string]any{"from": req.From, "to": req.To, "cc": req.CC, "bcc_count": len(req.BCC), "subject": req.Subject}, nil
}

func sendMailTLS(addr string, username string, password string, from string, to []string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", addr, 20*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	tc := textproto.NewConn(conn)
	if _, _, err := tc.ReadResponse(220); err != nil {
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
		return err
	}
	tc = textproto.NewConn(tlsConn)
	defer tc.Close()
	if err := smtpCommand(tc, 250, "EHLO localhost"); err != nil {
		return err
	}
	plain := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password))
	if err := smtpCommand(tc, 235, "AUTH PLAIN "+plain); err != nil {
		return err
	}
	if err := smtpCommand(tc, 250, "MAIL FROM:<"+from+">"); err != nil {
		return err
	}
	for _, addr := range to {
		if err := smtpCommand(tc, 250, "RCPT TO:<"+addr+">"); err != nil {
			return err
		}
	}
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
		return err
	}
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
	return err
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
