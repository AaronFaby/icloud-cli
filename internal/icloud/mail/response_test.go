package mail

import (
	"strings"
	"testing"

	"github.com/aaronfaby/icloud-cli/internal/config"
)

func TestPrepareReplyUsesReplyToAndThreading(t *testing.T) {
	source := Message{MessageSummary: MessageSummary{
		ID:         "123",
		Folder:     "INBOX",
		Subject:    "Hello",
		From:       "Sender <sender@example.com>",
		ReplyTo:    []string{"Replies <reply@example.com>"},
		To:         []string{"me@example.com"},
		Date:       "Sun, 14 Jun 2026 10:00:00 -0700",
		MessageID:  "<source@example.com>",
		References: "<root@example.com>",
	}, Body: "original body"}
	prepared, err := PrepareResponse(config.Config{AppleID: "me@example.com"}, source, ResponseReply, ResponseInput{Text: "Thanks"}, "send")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Subject != "Re: Hello" {
		t.Fatalf("subject = %q", prepared.Subject)
	}
	if len(prepared.To) != 1 || prepared.To[0] != "Replies <reply@example.com>" {
		t.Fatalf("to = %#v", prepared.To)
	}
	if prepared.Headers["In-Reply-To"] != "<source@example.com>" {
		t.Fatalf("in-reply-to = %q", prepared.Headers["In-Reply-To"])
	}
	if prepared.Headers["References"] != "<root@example.com> <source@example.com>" {
		t.Fatalf("references = %q", prepared.Headers["References"])
	}
	if !strings.Contains(prepared.Request.Text, "On Sun, 14 Jun 2026 10:00:00 -0700, Sender <sender@example.com> wrote:") {
		t.Fatalf("reply text missing attribution: %q", prepared.Request.Text)
	}
	if !strings.Contains(prepared.Request.Text, "> original body") {
		t.Fatalf("reply text missing quote: %q", prepared.Request.Text)
	}
}

func TestPrepareReplyAllDeduplicatesAndExcludesSender(t *testing.T) {
	source := Message{MessageSummary: MessageSummary{
		ID:      "123",
		Folder:  "INBOX",
		Subject: "Re: Hello",
		From:    "Sender <sender@example.com>",
		To:      []string{"Me <me@example.com>", "Other <other@example.com>", "Sender <sender@example.com>"},
		CC:      []string{"Other <other@example.com>", "CC <cc@example.com>"},
	}}
	prepared, err := PrepareResponse(config.Config{AppleID: "me@example.com"}, source, ResponseReplyAll, ResponseInput{Text: "Thanks", CC: []string{"Extra <extra@example.com>"}}, "send")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Subject != "Re: Hello" {
		t.Fatalf("subject = %q", prepared.Subject)
	}
	wantTo := strings.Join([]string{"Sender <sender@example.com>", "Other <other@example.com>"}, "\n")
	if strings.Join(prepared.To, "\n") != wantTo {
		t.Fatalf("to = %#v", prepared.To)
	}
	wantCC := strings.Join([]string{"CC <cc@example.com>", "Extra <extra@example.com>"}, "\n")
	if strings.Join(prepared.CC, "\n") != wantCC {
		t.Fatalf("cc = %#v", prepared.CC)
	}
}

func TestPrepareForwardUsesFwdSubjectAndNoReplyThreading(t *testing.T) {
	source := Message{MessageSummary: MessageSummary{
		ID:        "123",
		Folder:    "INBOX",
		Subject:   "Fwd: Hello",
		From:      "Sender <sender@example.com>",
		To:        []string{"me@example.com"},
		Date:      "Sun, 14 Jun 2026 10:00:00 -0700",
		MessageID: "<source@example.com>",
	}, Body: "original body"}
	prepared, err := PrepareResponse(config.Config{AppleID: "me@example.com"}, source, ResponseForward, ResponseInput{To: []string{"me@example.com"}, CC: []string{"me@example.com", "copy@example.com"}, Text: "FYI"}, "send")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Subject != "Fwd: Hello" {
		t.Fatalf("subject = %q", prepared.Subject)
	}
	if prepared.Headers["In-Reply-To"] != "" || prepared.Headers["References"] != "" {
		t.Fatalf("forward used reply threading headers: %#v", prepared.Headers)
	}
	if prepared.Headers["Message-ID"] == "" {
		t.Fatalf("missing message-id: %#v", prepared.Headers)
	}
	if got, want := strings.Join(prepared.To, ","), "me@example.com"; got != want {
		t.Fatalf("to = %q, want %q", got, want)
	}
	if got, want := strings.Join(prepared.CC, ","), "copy@example.com"; got != want {
		t.Fatalf("cc = %q, want %q", got, want)
	}
	if !strings.Contains(prepared.Request.Text, "---------- Forwarded message ---------") || !strings.Contains(prepared.Request.Text, "> original body") {
		t.Fatalf("forward body = %q", prepared.Request.Text)
	}
}

func TestPrepareResponseValidation(t *testing.T) {
	source := Message{MessageSummary: MessageSummary{ID: "123", Folder: "INBOX", From: "sender@example.com"}}
	if _, err := PrepareResponse(config.Config{AppleID: "me@example.com"}, source, ResponseReply, ResponseInput{}, "send"); err == nil {
		t.Fatal("expected missing text error")
	}
	if _, err := PrepareResponse(config.Config{AppleID: "me@example.com"}, source, ResponseForward, ResponseInput{Text: "FYI"}, "send"); err == nil {
		t.Fatal("expected missing forward recipient error")
	}
}

func TestExtractReadableTextDecodesTransferEncodingsAndMultipart(t *testing.T) {
	qp := Message{Raw: "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nhello=20world\r\n"}
	if got := extractReadableText(qp); !strings.Contains(got, "hello world") {
		t.Fatalf("quoted-printable text = %q", got)
	}
	b64 := Message{Raw: "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\naGVsbG8gd29ybGQ=\r\n"}
	if got := extractReadableText(b64); !strings.Contains(got, "hello world") {
		t.Fatalf("base64 text = %q", got)
	}
	multipart := Message{Raw: "Content-Type: multipart/alternative; boundary=abc\r\n\r\n--abc\r\nContent-Type: text/html\r\n\r\n<p>html</p>\r\n--abc\r\nContent-Type: text/plain\r\n\r\nplain body\r\n--abc--\r\n"}
	if got := extractReadableText(multipart); !strings.Contains(got, "plain body") {
		t.Fatalf("multipart text = %q", got)
	}
}
