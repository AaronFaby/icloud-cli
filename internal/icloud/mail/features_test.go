package mail

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/aaronfaby/icloud-cli/internal/config"
)

func TestOutgoingAttachmentsAndForwardPrivacy(t *testing.T) {
	req := SendRequest{From: "me@example.com", To: []string{"to@example.com"}, BCC: []string{"secret@example.com"}, Text: "plain", HTML: "<p>html</p>", Attachments: []SendAttachment{{Filename: "résumé.txt", ContentBase64: base64.StdEncoding.EncodeToString([]byte("file bytes"))}}}
	raw, err := buildMessage(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret@example.com") {
		t.Fatal("BCC leaked")
	}
	parsed, err := parseMIME(string(raw), "*")
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.files) != 1 || parsed.files[0].Filename != "résumé.txt" || parsed.files[0].ContentBase64 != req.Attachments[0].ContentBase64 {
		t.Fatalf("bad mixed/alternative roundtrip: %+v", parsed)
	}
	bodyParsed, err := parseMIME(string(raw), "")
	if err != nil || len(bodyParsed.textParts) != 1 || len(bodyParsed.htmlParts) != 1 {
		t.Fatal("missing alternative bodies", err)
	}
	source := Message{MessageSummary: MessageSummary{From: "sender@example.com"}, Raw: string(raw)}
	prepared, err := PrepareResponse(config.Config{AppleID: "me@example.com"}, source, ResponseForward, ResponseInput{To: []string{"other@example.com"}, Text: "forward", IncludeAttachments: true}, "send")
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Request.Attachments) != 1 || len(prepared.Attachments) != 1 {
		t.Fatal("missing forwarded attachment")
	}
	preview, _ := json.Marshal(prepared)
	if strings.Contains(string(preview), "content_base64") || strings.Contains(string(preview), "file bytes") || strings.Contains(string(preview), "\"path\"") {
		t.Fatal("preview leaks attachment content")
	}
	prepared, err = PrepareResponse(config.Config{AppleID: "me@example.com"}, source, ResponseForward, ResponseInput{To: []string{"other@example.com"}, Text: "forward"}, "send")
	if err != nil || len(prepared.Request.Attachments) != 0 {
		t.Fatal("default must omit source files", err)
	}
}

func TestOutgoingAttachmentValidation(t *testing.T) {
	for _, a := range []SendAttachment{{Path: "/not/read", ContentBase64: "YQ=="}, {Filename: "bad\r\nname", ContentBase64: "YQ=="}, {ContentBase64: "!"}, {ContentType: "multipart/mixed", ContentBase64: "YQ=="}, {Path: "."}} {
		if _, _, err := resolveAttachments([]SendAttachment{a}); err == nil {
			t.Fatalf("accepted %+v", a)
		}
	}
}

func TestPollSnapshotPagingAndReset(t *testing.T) {
	state := []string{"* OK [UIDVALIDITY 9] stable", "* OK [UIDNEXT 8] next", "A0001 OK selected"}
	lines := append([]string{}, state...)
	lines = append(lines, "* SEARCH 7 1 3", "A0001 OK search", "* 1 FETCH (UID 1 BODY[HEADER] {18}", "Subject: first\r\n\r\n)", "A0001 OK fetch")
	lines = append(lines, state...)
	c, commands := newScriptedIMAPClient(t, lines)
	defer c.conn.Close()
	first, err := c.PollMessages("me@example.com", PollOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Messages) != 1 || first.Messages[0].ID != "1" {
		t.Fatalf("%+v", first)
	}
	for range commands {
	}
	state[1] = "* OK [UIDNEXT 10] next"
	lines = append([]string{}, state...)
	lines = append(lines, "* SEARCH 3 7 9", "A0001 OK search", "A0001 OK expunged", "* 2 FETCH (UID 7 BODY[HEADER] {17}", "Subject: last\r\n\r\n)", "A0001 OK fetch")
	lines = append(lines, state...)
	c2, _ := newScriptedIMAPClient(t, lines)
	defer c2.conn.Close()
	second, err := c2.PollMessages("me@example.com", PollOptions{Cursor: first.NextCursor, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || len(second.Messages) != 1 || second.Messages[0].ID != "7" {
		t.Fatalf("%+v", second)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(second.NextCursor)
	var cursor pollCursor
	json.Unmarshal(raw, &cursor)
	if cursor.Next != 8 || cursor.Boundary != 8 {
		t.Fatalf("snapshot advanced: %+v", cursor)
	}
	c3, _ := newScriptedIMAPClient(t, []string{"* OK [UIDVALIDITY 10] reset", "* OK [UIDNEXT 10] next", "A0001 OK selected"})
	defer c3.conn.Close()
	if _, err := c3.PollMessages("me@example.com", PollOptions{Cursor: second.NextCursor}); err == nil {
		t.Fatal("accepted reset")
	}
}

func TestPollRejectsIncompleteResponsesAndSupportsStartNow(t *testing.T) {
	state := []string{"* OK [UIDVALIDITY 9] stable", "* OK [UIDNEXT 4] text [UIDNEXT 100]", "A0001 OK selected"}
	for _, search := range [][]string{{"A0001 OK search"}, {"* SEARCH invalidUID", "A0001 OK search"}, {"* SEARCH 0", "A0001 OK search"}, {"A0001 NO failed"}} {
		c, _ := newScriptedIMAPClient(t, append(append([]string{}, state...), search...))
		result, err := c.PollMessages("me", PollOptions{})
		c.conn.Close()
		if err == nil || result.NextCursor != "" {
			t.Fatalf("advanced invalid response: %+v, %v", result, err)
		}
	}
	c, _ := newScriptedIMAPClient(t, append(append([]string{}, state...), state...))
	defer c.conn.Close()
	result, err := c.PollMessages("me", PollOptions{StartNow: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(result.NextCursor)
	var cursor pollCursor
	json.Unmarshal(raw, &cursor)
	if cursor.Next != 4 || cursor.Boundary != 4 || len(result.Messages) != 0 {
		t.Fatalf("%+v", cursor)
	}
	if _, err := c.PollMessages("other", PollOptions{Cursor: result.NextCursor}); err == nil {
		t.Fatal("accepted wrong account")
	}
	if _, err := c.PollMessages("me", PollOptions{Folder: "other", Cursor: result.NextCursor}); err == nil {
		t.Fatal("accepted wrong folder")
	}
}

func TestPollFetchFailureDoesNotAdvance(t *testing.T) {
	c, _ := newScriptedIMAPClient(t, []string{"* OK [UIDVALIDITY 9] stable", "* OK [UIDNEXT 4] next", "A0001 OK selected", "* SEARCH 1", "A0001 OK search", "A0001 NO failed"})
	defer c.conn.Close()
	result, err := c.PollMessages("me", PollOptions{})
	if err == nil || result.NextCursor != "" {
		t.Fatal("fetch failure advanced cursor")
	}
}

func TestAttachmentPathLoadsOnlyRegularFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(file, []byte("report"), 0600); err != nil {
		t.Fatal(err)
	}
	attachments, previews, err := resolveAttachments([]SendAttachment{{Path: file}})
	if err != nil || len(attachments) != 1 || attachments[0].Path != "" || previews[0].Size != 6 {
		t.Fatal("file not resolved", err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveAttachments([]SendAttachment{{Path: fifo}}); err == nil {
		t.Fatal("accepted FIFO")
	}
}

func TestOutgoingAttachmentAndEncodedLimits(t *testing.T) {
	if _, _, err := resolveAttachments(make([]SendAttachment, 101)); err == nil {
		t.Fatal("accepted 101 attachments")
	}
	half := base64.StdEncoding.EncodeToString(make([]byte, (maxOutgoingAttachmentBytes/2)+1))
	if _, _, err := resolveAttachments([]SendAttachment{{ContentBase64: half}, {ContentBase64: half}}); err == nil {
		t.Fatal("accepted aggregate attachments over 20 MiB")
	}
	// Quoted-printable expands these bytes; the input fits but the wire image does not.
	req := SendRequest{From: "me@example.com", To: []string{"to@example.com"}, Text: strings.Repeat("\x01", maxMessageBytes/3+1)}
	if _, err := buildMessage(req); err == nil {
		t.Fatal("accepted encoded message over 32 MiB")
	}
}

func TestPollResetAtEndReturnsNoCursor(t *testing.T) {
	c, _ := newScriptedIMAPClient(t, []string{"* OK [UIDVALIDITY 9] stable", "* OK [UIDNEXT 4] next", "A0001 OK selected", "* SEARCH", "A0001 OK search", "* OK [UIDVALIDITY 10] reset", "* OK [UIDNEXT 4] next", "A0001 OK selected"})
	defer c.conn.Close()
	result, err := c.PollMessages("me", PollOptions{})
	if err == nil || result.NextCursor != "" {
		t.Fatal("final reset advanced cursor")
	}
}
