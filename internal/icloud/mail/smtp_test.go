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

func TestBuildMessageAllowsThreadingHeaders(t *testing.T) {
	msg, err := buildMessage(SendRequest{
		From:    "sender@example.com",
		To:      []string{"one@example.com"},
		Subject: "reply",
		Text:    "body",
		Headers: map[string]string{
			"Message-ID":   "<new@example.com>",
			"In-Reply-To":  "<source@example.com>",
			"References":   "<root@example.com> <source@example.com>",
			"MIME-Version": "spoof",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(msg)
	for _, want := range []string{
		"Message-ID: <new@example.com>\r\n",
		"In-Reply-To: <source@example.com>\r\n",
		"References: <root@example.com> <source@example.com>\r\n",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("message missing %q in:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "MIME-Version: spoof") {
		t.Fatalf("message accepted MIME-Version override: %s", raw)
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

func TestEnvelopeAddressStripsDisplayName(t *testing.T) {
	tests := map[string]string{
		"sender@example.com":           "sender@example.com",
		"Name <sender@example.com>":    "sender@example.com",
		`"Quoted Name" <a@b.example>`:  "a@b.example",
		"  Name <user@icloud.com>  ":   "user@icloud.com",
	}
	for raw, want := range tests {
		got, err := envelopeAddress(raw)
		if err != nil {
			t.Fatalf("envelopeAddress(%q) error: %v", raw, err)
		}
		if got != want {
			t.Fatalf("envelopeAddress(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestEnvelopeAddressRejectsInvalid(t *testing.T) {
	for _, raw := range []string{"", "not-an-address", "Name <", "a b@c.com"} {
		if _, err := envelopeAddress(raw); err == nil {
			t.Fatalf("envelopeAddress(%q) expected error", raw)
		}
	}
}

func TestEncodeHeaderStripsCRLF(t *testing.T) {
	got := encodeHeader("hello\r\nBcc: attacker@example.com")
	if strings.Contains(got, "\r") || strings.Contains(got, "\n") {
		t.Fatalf("encodeHeader leaked newline: %q", got)
	}
	if strings.Contains(got, "Bcc:") && strings.Contains(got, "\n") {
		t.Fatalf("encodeHeader allowed header injection: %q", got)
	}
	// Sanitized to a single-line value (newline became space).
	if !strings.Contains(got, "Bcc: attacker@example.com") {
		// After sanitize, Bcc text remains as content of subject — that's fine;
		// the important property is no CRLF to start a new header.
		t.Fatalf("unexpected encodeHeader output: %q", got)
	}

	msg, err := buildMessage(SendRequest{
		From:    "sender@example.com",
		To:      []string{"one@example.com"},
		Subject: "safe\r\nBcc: evil@example.com",
		Text:    "body",
		Headers: map[string]string{
			"X-Agent-Test": "ok\r\nX-Injected: yes",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(msg)
	if strings.Contains(raw, "\nBcc:") || strings.Contains(raw, "\nX-Injected:") {
		t.Fatalf("message allowed header injection:\n%s", raw)
	}
}
