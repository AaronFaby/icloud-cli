package mail

import (
	"net/mail"
	"strings"
	"testing"
)

func TestBuildMessageEncodesUnicodeSubjectAndRecipients(t *testing.T) {
	msg, err := buildMessage(SendRequest{
		From:    "sender@example.com",
		To:      []string{"one@example.com", "two@example.com"},
		CC:      []string{"cc@example.com"},
		BCC:     []string{"hidden@example.com"},
		Subject: "Hello 日本語",
		Text:    "plain body",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(msg)
	if strings.Contains(raw, "Bcc:") || strings.Contains(raw, "hidden@example.com") {
		t.Fatalf("message leaked bcc: %s", raw)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Header.Get("From"); got != "sender@example.com" {
		t.Fatalf("from = %q", got)
	}
	if got := parsed.Header.Get("To"); got != "one@example.com, two@example.com" {
		t.Fatalf("to = %q", got)
	}
	if got := parsed.Header.Get("Cc"); got != "cc@example.com" {
		t.Fatalf("cc = %q", got)
	}
	if got := parsed.Header.Get("Subject"); !strings.HasPrefix(strings.ToLower(got), "=?utf-8?") {
		t.Fatalf("subject = %q, want encoded word", got)
	}
}

func TestBuildMessageIgnoresProtectedHeaders(t *testing.T) {
	msg, err := buildMessage(SendRequest{
		From:    "sender@example.com",
		To:      []string{"one@example.com"},
		Subject: "test",
		Text:    "body",
		Headers: map[string]string{
			"From":         "spoof@example.com",
			"To":           "other@example.com",
			"Subject":      "spoofed",
			"Content-Type": "text/html",
			"X-Agent-Test": "ok",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(msg)
	if strings.Contains(raw, "spoof@example.com") || strings.Contains(raw, "other@example.com") || strings.Contains(raw, "Subject: spoofed") {
		t.Fatalf("message accepted protected header override: %s", raw)
	}
	if !strings.Contains(raw, "X-Agent-Test: ok\r\n") {
		t.Fatalf("message missing custom header: %s", raw)
	}
	if !strings.Contains(raw, "Content-Type: text/plain; charset=utf-8\r\n") {
		t.Fatalf("message content type was overridden: %s", raw)
	}
}

func TestBuildMessageCreatesMultipartAlternative(t *testing.T) {
	msg, err := buildMessage(SendRequest{
		From:    "sender@example.com",
		To:      []string{"one@example.com"},
		Subject: "html",
		Text:    "plain",
		HTML:    "<p>html</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(msg)
	if !strings.Contains(raw, "Content-Type: multipart/alternative;") {
		t.Fatalf("message is not multipart alternative: %s", raw)
	}
	if !strings.Contains(raw, "Content-Type: text/plain; charset=utf-8") || !strings.Contains(raw, "Content-Type: text/html; charset=utf-8") {
		t.Fatalf("message missing multipart parts: %s", raw)
	}
}

func TestSMTPCommandNameRedactsAddressCommands(t *testing.T) {
	tests := map[string]string{
		"AUTH PLAIN abc123":              "AUTH",
		"MAIL FROM:<sender@example.com>": "MAIL FROM",
		"RCPT TO:<one@example.com>":      "RCPT TO",
		"DATA":                           "DATA",
	}
	for line, want := range tests {
		if got := smtpCommandName(line); got != want {
			t.Fatalf("smtpCommandName(%q) = %q, want %q", line, got, want)
		}
	}
}
