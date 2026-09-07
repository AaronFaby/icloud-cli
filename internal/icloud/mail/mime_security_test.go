package mail

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aaronfaby/icloud-cli/internal/config"
	"golang.org/x/net/html"
)

func TestHTMLAllowlistBlocksActiveContentAndPreservesFormatting(t *testing.T) {
	input := `<p title="hello">Hello <strong>reader</strong></p><table><tr><td colspan="2">cell</td></tr></table><a href="https://example.com/?q=a&amp;b=c">link</a><img src="cid:part1" alt="photo">` +
		`<a href="java&#115;cript:alert(1)" onclick="secret()">bad</a><a href="java&#x09;script:alert(1)">bad2</a>` +
		`<iframe srcdoc="&lt;script>alert(1)&lt;/script>"></iframe><svg><a xlink:href="javascript:alert(1)">svg</a></svg>` +
		`<math><mtext><table><mglyph><style><!--</style><img title="--><img src=x onerror=alert(1)>">` +
		`<object data="https://example.com"></object><form action="https://evil.example"><input autofocus onfocus="bad()"></form>` +
		`<div style="background:url(javascript:bad())">visible</div><img src="data:image/svg+xml,evil"><script>secret()</script>`
	got, _, err := sanitizeHTML(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<strong>reader</strong>`, `<td colspan="2">cell</td>`, `href="https://example.com/?q=a&amp;b=c"`, `src="cid:part1"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost useful formatting %q: %s", want, got)
		}
	}
	if strings.Contains(got, "secret()") {
		t.Fatalf("script content survived: %s", got)
	}
	// Reparse the serialized output, as a consumer would. Check DOM semantics,
	// rather than requiring unsafe strings to vanish from escaped text.
	doc, err := html.Parse(strings.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	for n := range doc.Descendants() {
		if n.Namespace != "" {
			t.Fatalf("foreign namespace survived: %#v", n)
		}
		switch n.Data {
		case "script", "style", "iframe", "object", "embed", "form", "input", "svg", "math":
			if n.Type == html.ElementNode {
				t.Fatalf("active element survived: %s", n.Data)
			}
		}
		for _, a := range n.Attr {
			if strings.HasPrefix(a.Key, "on") || a.Key == "srcdoc" || a.Key == "style" || a.Namespace != "" {
				t.Fatalf("active attribute survived: %#v", a)
			}
			if a.Key == "href" || a.Key == "src" {
				if !safeHTMLURL(a.Val, n.Data == "img") {
					t.Fatalf("unsafe URL survived: %q", a.Val)
				}
			}
		}
	}
	if again, _, err := sanitizeHTML(got); err != nil || again != got {
		t.Fatalf("sanitizer changed on reparse:\n%s\n%s", got, again)
	}
}

func TestMIMECharsetDecodingAndErrors(t *testing.T) {
	for _, tc := range []struct{ charset, body, want string }{
		{"iso-8859-1", "caf=E9", "café"},
		{"windows-1252", "=93hello=94", "“hello”"},
		{"utf-8", "caf=C3=A9", "café"},
		{"shift_jis", "=82=B1=82=F1=82=C9=82=BF=82=CD", "こんにちは"},
	} {
		t.Run(tc.charset, func(t *testing.T) {
			raw := "Content-Type: text/plain; charset=" + tc.charset + "\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" + tc.body
			content, err := parseMessageContent(raw)
			if err != nil || content.Text != tc.want || !utf8.ValidString(content.Text) {
				t.Fatalf("content=%#v err=%v", content, err)
			}
			source := Message{MessageSummary: MessageSummary{From: "sender@example.com"}, Raw: raw}
			response, err := PrepareResponse(config.Config{AppleID: "me@example.com"}, source, ResponseReply, ResponseInput{Text: "Thanks"}, "send")
			if err != nil || !strings.Contains(response.Request.Text, "> "+tc.want) {
				t.Fatalf("response=%#v err=%v", response, err)
			}
		})
	}
	for _, raw := range []string{
		"Content-Type: text/plain; charset=x-unknown\r\n\r\nbody",
		"Content-Type: text/plain; charset=utf-8\r\n\r\ncaf\xe9",
		"Content-Type: text/plain; charset=us-ascii\r\n\r\ncaf\xe9",
		"Content-Type: text/plain; bad\r\n\r\nbody",
		"Content-Transfer-Encoding: base64\r\n\r\n!invalid!",
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n\x00",
		"Content-Transfer-Encoding: x-unknown\r\n\r\nbody",
		"Content-Type: multipart/mixed\r\n\r\nbody",
		"Content-Type: multipart/mixed; boundary=a\r\n\r\n--a\r\n\r\ntruncated",
	} {
		if _, err := parseMessageContent(raw); err == nil {
			t.Errorf("accepted corrupt/unsupported MIME: %q", raw)
		}
		source := Message{MessageSummary: MessageSummary{From: "sender@example.com"}, Raw: raw, Body: "fallback must not hide corruption"}
		if _, err := PrepareResponse(config.Config{AppleID: "me@example.com"}, source, ResponseReply, ResponseInput{Text: "Thanks"}, "send"); err == nil {
			t.Errorf("reply silently ignored corrupt MIME: %q", raw)
		}
	}
}

func TestAttachmentDecodeErrorsAndUnrelatedCharset(t *testing.T) {
	prefix := "Content-Type: multipart/mixed; boundary=a\r\n\r\n--a\r\nContent-Type: text/plain; charset=x-unknown\r\n\r\nbody\r\n--a\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=file.bin\r\nContent-Transfer-Encoding: base64\r\n\r\n"
	good := prefix + "aGVsbG8=\r\n--a--\r\n"
	file, ok, err := attachmentWithContent(good, "1")
	if err != nil || !ok || file.ContentBase64 != "aGVsbG8=" || file.Size != 5 {
		t.Fatalf("file=%#v ok=%v err=%v", file, ok, err)
	}
	if _, _, err := attachmentWithContent(prefix+"bad!\r\n--a--\r\n", "1"); err == nil {
		t.Fatal("corrupt attachment returned successfully")
	}
}

func TestHTMLSemanticContainersKeepMessageBody(t *testing.T) {
	raw := "Content-Type: text/html; charset=utf-8\r\n\r\n<main><article><header>From the team</header><section><p>Message <em>body</em></p></section><footer>Thanks</footer></article></main>"
	content, err := parseMessageContent(raw)
	if err != nil || !strings.Contains(content.Text, "Message body") || !strings.Contains(content.HTML, "<em>body</em>") {
		t.Fatalf("lost message content: %#v err=%v", content, err)
	}
}

func TestMIMEInlineImageWithoutFilenameIsRetrievable(t *testing.T) {
	raw := "Content-Type: multipart/related; boundary=a\r\n\r\n--a\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:image1\">\r\n--a\r\nContent-Type: image/png\r\nContent-ID: <image1>\r\nContent-Disposition: inline\r\nContent-Transfer-Encoding: base64\r\n\r\naGVsbG8=\r\n--a--\r\n"
	content, err := parseMessageContent(raw)
	if err != nil || len(content.Attachments) != 1 || !content.Attachments[0].Inline || content.Attachments[0].ContentID != "image1" {
		t.Fatalf("content=%#v err=%v", content, err)
	}
	file, ok, err := attachmentWithContent(raw, content.Attachments[0].ID)
	if err != nil || !ok || file.ContentBase64 != "aGVsbG8=" {
		t.Fatalf("file=%#v ok=%v err=%v", file, ok, err)
	}
}
