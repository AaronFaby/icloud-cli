package mail

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/aaronfaby/icloud-cli/internal/config"
)

func TestDenseHTMLRejectsBeforeDOMAllocation(t *testing.T) {
	unit := "<span>x</span>"
	body := strings.Repeat(unit, maxHTMLBytes/len(unit))
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, _, err := sanitizeHTML(body)
	runtime.ReadMemStats(&after)
	if err == nil || !strings.Contains(err.Error(), "50000 tokens") {
		t.Fatalf("dense HTML was not rejected: %v", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
		t.Fatalf("dense HTML allocated %d bytes before rejection", allocated)
	} else {
		t.Logf("dense HTML rejected before DOM construction after %d allocated bytes", allocated)
	}

	// Exercise the actual body/reply parser too: it must return the error, never
	// success with a silently empty HTML/text body.
	raw := "Content-Type: text/html; charset=utf-8\r\n\r\n" + body
	runtime.GC()
	runtime.ReadMemStats(&before)
	content, err := parseMessageContent(raw)
	runtime.ReadMemStats(&after)
	if err == nil || content.HTML != "" || content.Text != "" {
		t.Fatalf("MIME result=%#v err=%v", content, err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 48<<20 {
		t.Fatalf("MIME HTML rejection allocated %d bytes", allocated)
	} else {
		t.Logf("full MIME HTML rejection allocated %d bytes", allocated)
	}
	source := Message{MessageSummary: MessageSummary{From: "sender@example.com"}, Raw: raw}
	if _, err := PrepareResponse(config.Config{AppleID: "me@example.com"}, source, ResponseReply, ResponseInput{Text: "Thanks"}, "send"); err == nil {
		t.Fatal("reply hid the HTML limit error")
	}
}

func TestHTMLInputTokenAndRenderedLimits(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"input bytes", strings.Repeat("x", maxHTMLBytes+1), "HTML input exceeds 4 MiB"},
		{"single token", "<p>" + strings.Repeat("x", maxHTMLTokenBytes+1) + "</p>", "token exceeds 1 MiB"},
		{"rendered bytes", strings.Repeat("&", 900000), "sanitized HTML exceeds 4 MiB"},
		{"rendered token", strings.Repeat("&", 300000), "token exceeds 1 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, text, err := sanitizeHTML(tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) || out != "" || text != "" {
				t.Fatalf("limit result: html=%d text=%d err=%v", len(out), len(text), err)
			}
		})
	}
	raw := "Content-Type: text/html; charset=windows-1252\r\n\r\n" + strings.Repeat("\x80", 2<<20)
	if _, err := parseMessageContent(raw); err == nil || !strings.Contains(err.Error(), "HTML input exceeds 4 MiB") {
		t.Fatalf("charset expansion bypassed HTML input limit: %v", err)
	}
	// A practical formatted message well below the caps still yields both forms.
	out, text, err := sanitizeHTML("<main><p>Hello <strong>reader</strong>.</p><p><a href=\"https://example.com\">Details</a></p></main>")
	if err != nil || !strings.Contains(out, "<strong>reader</strong>") || !strings.Contains(text, "Hello reader.") || !strings.Contains(text, "Details") {
		t.Fatalf("ordinary HTML result: %q %q %v", out, text, err)
	}
}

func TestHTMLAttributeCountSharesTokenBudget(t *testing.T) {
	var body strings.Builder
	body.WriteString("<span")
	for i := 0; i < maxHTMLTokens; i++ {
		fmt.Fprintf(&body, " a%d=x", i)
	}
	body.WriteString(">x</span>")
	if _, _, err := sanitizeHTML(body.String()); err == nil || !strings.Contains(err.Error(), "tokens or attributes") {
		t.Fatalf("attribute budget bypass: %v", err)
	}
}

func TestHTMLBudgetCountsMarkupInsideForeignRawTextContexts(t *testing.T) {
	body := "<svg><title>" + strings.Repeat("<span>x</span>", maxHTMLTokens/2) + "</title></svg>"
	if _, _, err := sanitizeHTML(body); err == nil || !strings.Contains(err.Error(), "tokens or attributes") {
		t.Fatalf("foreign raw-text context bypassed preflight token budget: %v", err)
	}
}
