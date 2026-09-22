package mail

import (
	"bufio"
	"context"
	"errors"
	"io"
	netmail "net/mail"
	"time"

	"fmt"
	"github.com/aaronfaby/icloud-cli/internal/output"
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestChooseTrashFolderPrefersTrashFlag(t *testing.T) {
	got := chooseTrashFolder([]Folder{
		{Name: "Trash"},
		{Name: "Deleted Messages", Flags: []string{`\Trash`}},
	}, DefaultTrash)
	if got != "Deleted Messages" {
		t.Fatalf("trash folder = %q, want Deleted Messages", got)
	}
}

func TestChooseTrashFolderKeepsExplicitFolder(t *testing.T) {
	got := chooseTrashFolder([]Folder{{Name: "Deleted Messages", Flags: []string{`\Trash`}}}, "Custom Trash")
	if got != "Custom Trash" {
		t.Fatalf("trash folder = %q, want explicit folder", got)
	}
}

func TestChooseTrashFolderFallsBackToName(t *testing.T) {
	got := chooseTrashFolder([]Folder{{Name: "Deleted Messages"}}, "")
	if got != "Deleted Messages" {
		t.Fatalf("trash folder = %q, want Deleted Messages", got)
	}
}

func TestChooseSentFolderPrefersSentFlag(t *testing.T) {
	got := chooseSentFolder([]Folder{
		{Name: "Sent"},
		{Name: "Sent Messages", Flags: []string{`\Sent`}},
	}, "")
	if got != "Sent Messages" {
		t.Fatalf("sent folder = %q, want Sent Messages", got)
	}
}

func TestChooseSentFolderKeepsExplicitFolder(t *testing.T) {
	got := chooseSentFolder([]Folder{{Name: "Sent Messages", Flags: []string{`\Sent`}}}, "Archive/Sent")
	if got != "Archive/Sent" {
		t.Fatalf("sent folder = %q, want explicit folder", got)
	}
}

func TestChooseSentFolderFallsBackToName(t *testing.T) {
	got := chooseSentFolder([]Folder{{Name: "Sent Items"}}, "")
	if got != "Sent Items" {
		t.Fatalf("sent folder = %q, want Sent Items", got)
	}
}

func TestChooseDraftFolder(t *testing.T) {
	if got := chooseDraftFolder([]Folder{{Name: "Draft Mail", Flags: []string{`\Drafts`}}}, ""); got != "Draft Mail" {
		t.Fatalf("draft folder = %q, want flagged folder", got)
	}
	if got := chooseDraftFolder([]Folder{{Name: "Drafts"}}, ""); got != "Drafts" {
		t.Fatalf("draft folder = %q, want named folder", got)
	}
	if got := chooseDraftFolder(nil, "Custom Drafts"); got != "Custom Drafts" {
		t.Fatalf("draft folder = %q, want explicit folder", got)
	}
}

func TestChooseSpecialFoldersIgnoreSubstringNames(t *testing.T) {
	if got := chooseSentFolder([]Folder{{Name: "Unsent"}, {Name: "Consent"}, {Name: "Sent Messages"}}, ""); got != "Sent Messages" {
		t.Fatalf("sent folder = %q, want Sent Messages", got)
	}
	if got := chooseSentFolder([]Folder{{Name: "Sentimental"}}, ""); got != "Sent" {
		t.Fatalf("sent folder = %q, want default Sent", got)
	}
	if got := chooseSentFolder([]Folder{{Name: "INBOX/Sent Items"}}, ""); got != "INBOX/Sent Items" {
		t.Fatalf("sent folder = %q, want delimited alias", got)
	}
	if got := chooseDraftFolder([]Folder{{Name: "Drafting Notes"}, {Name: "Draft Messages"}}, ""); got != "Draft Messages" {
		t.Fatalf("draft folder = %q, want Draft Messages", got)
	}
	if got := chooseTrashFolder([]Folder{{Name: "Trashcan"}, {Name: "Deleted Items"}}, ""); got != "Deleted Items" {
		t.Fatalf("trash folder = %q, want Deleted Items", got)
	}
}

func TestParseFolderHandlesEscapedAndUnicodeNames(t *testing.T) {
	tests := []struct {
		name string
		line string
		want Folder
	}{
		{
			name: "spaces and slash",
			line: `* LIST (\HasNoChildren) "/" "Projects/2026 Planning"`,
			want: Folder{Name: "Projects/2026 Planning", Delimiter: "/", Flags: []string{`\HasNoChildren`}},
		},
		{
			name: "escaped quote and ampersand",
			line: `* LIST (\HasNoChildren) "/" "Clients/ACME \"A&B\""`,
			want: Folder{Name: `Clients/ACME "A&B"`, Delimiter: "/", Flags: []string{`\HasNoChildren`}},
		},
		{
			name: "modified utf7",
			line: `* LIST (\HasNoChildren) "/" "Projects/&ZeVnLIqe-"`,
			want: Folder{Name: "Projects/日本語", Delimiter: "/", Flags: []string{`\HasNoChildren`}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFolder(tt.line)
			if got.Name != tt.want.Name || got.Delimiter != tt.want.Delimiter || strings.Join(got.Flags, ",") != strings.Join(tt.want.Flags, ",") {
				t.Fatalf("folder = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestEnsureCRLF(t *testing.T) {
	got := string(ensureCRLF([]byte("Subject: test\r\n\r\nbody")))
	if got != "Subject: test\r\n\r\nbody\r\n" {
		t.Fatalf("message = %q", got)
	}
	got = string(ensureCRLF([]byte("Subject: test\r\n")))
	if got != "Subject: test\r\n" {
		t.Fatalf("message = %q", got)
	}
}

func TestParseFetchReadsFlagsFromContinuationLine(t *testing.T) {
	// Protocol lines only: body literals are stored separately and must not appear in Lines.
	msg := parseFetch("INBOX", "183860", imapResponse{
		Lines: []string{
			`* 1 FETCH (UID 183860 RFC822.SIZE 42 BODY[HEADER] {10}`,
			` FLAGS (\Seen \Flagged) INTERNALDATE "09-Jun-2026 06:00:00 -0700")`,
			`A0001 OK Fetch completed`,
		},
		Literals: []string{"Subject: x\r\n\r\n"},
	}, FetchOptions{})
	if msg.ID != "183860" {
		t.Fatalf("id = %q", msg.ID)
	}
	if len(msg.Flags) != 2 || msg.Flags[0] != `\Seen` || msg.Flags[1] != `\Flagged` {
		t.Fatalf("flags = %#v", msg.Flags)
	}
}

func TestParseFetchHandlesMissingHeaders(t *testing.T) {
	msg := parseFetch("INBOX", "55", imapResponse{
		Lines: []string{
			`* 1 FETCH (UID 55 FLAGS () RFC822.SIZE 4 BODY[HEADER] {4}`,
			`A0001 OK Fetch completed`,
		},
		Literals: []string{"\r\n\r\n"},
	}, FetchOptions{})
	if msg.ID != "55" || msg.Folder != "INBOX" || msg.Subject != "" || msg.From != "" {
		t.Fatalf("message = %#v", msg)
	}
}

func TestParseFetchDecodesMimeHeaders(t *testing.T) {
	msg := parseFetch("INBOX", "55", imapResponse{
		Lines: []string{
			`* 1 FETCH (UID 55 FLAGS () RFC822.SIZE 100 BODY[HEADER] {100}`,
			`A0001 OK Fetch completed`,
		},
		Literals: []string{"Subject: =?utf-8?B?5pel5pys6Kqe?=\r\nFrom: =?utf-8?B?5bed5LiK?= <sender@example.com>\r\nTo: =?utf-8?B?5Y+X5L+h6ICF?= <to@example.com>\r\nCc: cc@example.com\r\nReply-To: reply@example.com\r\nReferences: <root@example.com>\r\nDate: Sun, 14 Jun 2026 10:00:00 -0700\r\n\r\n"},
	}, FetchOptions{RawHeaders: true})
	if msg.Subject != "日本語" || msg.RawSubject != "=?utf-8?B?5pel5pys6Kqe?=" {
		t.Fatalf("subject = %q raw = %q", msg.Subject, msg.RawSubject)
	}
	from, err := netmail.ParseAddress(msg.From)
	if err != nil || from.Name != "川上" || from.Address != "sender@example.com" || !strings.Contains(msg.From, "川上") || msg.RawFrom == "" {
		t.Fatalf("from = %q raw = %q", msg.From, msg.RawFrom)
	}
	if len(msg.To) != 1 {
		t.Fatalf("to = %#v", msg.To)
	}
	to, err := netmail.ParseAddress(msg.To[0])
	if err != nil || to.Name != "受信者" || to.Address != "to@example.com" || !strings.Contains(msg.To[0], "受信者") || msg.RawTo == "" || msg.RawDate == "" {
		t.Fatalf("to/raw = %#v raw_to=%q raw_date=%q", msg.To, msg.RawTo, msg.RawDate)
	}
	if len(msg.CC) != 1 || msg.CC[0] != "cc@example.com" || len(msg.ReplyTo) != 1 || msg.ReplyTo[0] != "reply@example.com" || msg.References != "<root@example.com>" {
		t.Fatalf("cc/reply-to/references = %#v %#v %q", msg.CC, msg.ReplyTo, msg.References)
	}
}

func TestParseFetchOmitsRawHeadersByDefault(t *testing.T) {
	msg := parseFetch("INBOX", "55", imapResponse{
		Lines:    []string{`* 1 FETCH (UID 55 FLAGS () RFC822.SIZE 10 BODY[HEADER] {10}`, `A0001 OK Fetch completed`},
		Literals: []string{"Subject: =?utf-8?B?5pel5pys6Kqe?=\r\n\r\n"},
	}, FetchOptions{})
	if msg.Subject != "日本語" || msg.RawSubject != "" {
		t.Fatalf("message = %#v", msg)
	}
}

func TestBuildSearchCriteriaForListFilters(t *testing.T) {
	got := buildSearchCriteria(MessageListOptions{
		Unread:  true,
		Flagged: true,
		Since:   "13-Jun-2026",
		From:    "domain.com",
	})
	want := `UNSEEN FLAGGED SINCE 13-Jun-2026 FROM "domain.com"`
	if got != want {
		t.Fatalf("criteria = %q, want %q", got, want)
	}
	if got := buildSearchCriteria(MessageListOptions{}); got != "ALL" {
		t.Fatalf("criteria = %q, want ALL", got)
	}
}

func TestSearchRejectsRawIMAPLiteral(t *testing.T) {
	var client IMAPClient
	for _, query := range []string{"TEXT {5}", "ALL {5}", "TEXT {5+}"} {
		_, err := client.Search("INBOX", query)
		var exit *output.ExitError
		if !errors.As(err, &exit) || exit.ExitCode != output.ExitValidation || exit.Err.Code != "unsupported_search_literal" {
			t.Fatalf("Search(%q) err = %#v", query, err)
		}
	}
	if containsIMAPLiteral(`TEXT "{5}"`) || containsIMAPLiteral(`SUBJECT "report \"{5}\""`) {
		t.Fatal("quoted brace text was treated as a literal")
	}
}

func TestSearchAllowsQuotedBraceText(t *testing.T) {
	client, commands := newScriptedIMAPClient(t, []string{
		`* 1 EXISTS`,
		`A0001 OK SELECT completed`,
		`* SEARCH`,
		`A0001 OK SEARCH completed`,
	})
	defer client.conn.Close()
	ids, err := client.Search("INBOX", `TEXT "{5}"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %#v", ids)
	}
	assertCommands(t, commands, []string{
		`A0001 SELECT "INBOX"`,
		`A0002 UID SEARCH TEXT "{5}"`,
	})
}

func TestPollCommandFailureIsRemote(t *testing.T) {
	cases := [][]string{
		{"A0001 NO failed"},
		{"* OK [UIDVALIDITY 9] stable", "* OK [UIDNEXT 4] next", "A0001 OK selected", "A0001 NO failed"},
		{"* OK [UIDVALIDITY 9] stable", "* OK [UIDNEXT 4] next", "A0001 OK selected", "* SEARCH 1", "A0001 OK search", "A0001 NO failed"},
		{"* OK [UIDVALIDITY 9] stable", "* OK [UIDNEXT 4] next", "A0001 OK selected", "* SEARCH", "A0001 OK search", "A0001 NO failed"},
	}
	for _, lines := range cases {
		client, _ := newScriptedIMAPClient(t, lines)
		_, err := client.PollMessages("me", PollOptions{})
		client.conn.Close()
		var exit *output.ExitError
		if !errors.As(err, &exit) || exit.ExitCode != output.ExitRemote {
			t.Fatalf("lines %#v err = %#v", lines, err)
		}
	}
}

func TestPollPreservesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client, _ := newScriptedIMAPClient(t, nil)
	defer client.conn.Close()
	client.ctx = ctx
	_, err := client.PollMessages("me", PollOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestLooksLikeIMAPCriteria(t *testing.T) {
	tests := map[string]bool{
		"UNSEEN":                true,
		`FROM "alerts@example"`: true,
		`SUBJECT "invoice"`:     true,
		`plain text search`:     false,
		`alerts@example.com`:    false,
		`UID 123:456`:           true,
		`SINCE 1-Jun-2026`:      true,
		`not-an-imap-criterion`: false,
		`Allison`:               false,
		`seenbefore`:            false,
		`TEXT "already quoted"`: true,
	}
	for query, want := range tests {
		if got := looksLikeIMAPCriteria(query); got != want {
			t.Fatalf("looksLikeIMAPCriteria(%q) = %v, want %v", query, got, want)
		}
	}
}

func TestDeleteDryRunDoesNotRequireConnection(t *testing.T) {
	var client *IMAPClient
	result, err := client.Delete("INBOX", "123", "Trash", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Warning != "dry_run:move_to_trash" {
		t.Fatalf("result = %#v", result)
	}

	result, err = client.Delete("INBOX", "123", "Trash", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Warning != "dry_run:permanent_delete" {
		t.Fatalf("result = %#v", result)
	}
}

func TestSetFlagCommands(t *testing.T) {
	client, commands := newScriptedIMAPClient(t, []string{
		`* 1 EXISTS`,
		`A0001 OK SELECT completed`,
		`A0002 OK STORE completed`,
		`* 1 EXISTS`,
		`A0003 OK SELECT completed`,
		`A0004 OK STORE completed`,
	})
	defer client.conn.Close()

	if err := client.SetFlag("INBOX", "123", `\Flagged`, true); err != nil {
		t.Fatal(err)
	}
	if err := client.SetFlag("INBOX", "123", `\Seen`, false); err != nil {
		t.Fatal(err)
	}

	want := []string{
		`A0001 SELECT "INBOX"`,
		`A0002 UID STORE 123 +FLAGS.SILENT (\Flagged)`,
		`A0003 SELECT "INBOX"`,
		`A0004 UID STORE 123 -FLAGS.SILENT (\Seen)`,
	}
	assertCommands(t, commands, want)
}

func TestResponseSourceFlagCommands(t *testing.T) {
	client, commands := newScriptedIMAPClient(t, []string{
		`* 1 EXISTS`,
		`A0001 OK SELECT completed`,
		`A0002 OK STORE completed`,
		`* 1 EXISTS`,
		`A0003 OK SELECT completed`,
		`A0004 OK STORE completed`,
	})
	defer client.conn.Close()

	if err := client.SetFlag("INBOX", "123", `\Answered`, true); err != nil {
		t.Fatal(err)
	}
	if err := client.SetFlag("INBOX", "123", `$Forwarded`, true); err != nil {
		t.Fatal(err)
	}

	want := []string{
		`A0001 SELECT "INBOX"`,
		`A0002 UID STORE 123 +FLAGS.SILENT (\Answered)`,
		`A0003 SELECT "INBOX"`,
		`A0004 UID STORE 123 +FLAGS.SILENT ($Forwarded)`,
	}
	assertCommands(t, commands, want)
}

func TestAppendDraftUsesDraftFlag(t *testing.T) {
	server, clientConn := net.Pipe()
	commands := make(chan string, 2)
	go func() {
		defer close(commands)
		defer server.Close()
		reader := bufio.NewReader(server)
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		commands <- line
		tag := strings.Fields(line)[0]
		_, _ = server.Write([]byte(`* LIST (\Drafts) "/" "Drafts"` + "\r\n" + tag + " OK LIST completed\r\n"))

		line, err = reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		commands <- line
		tag = strings.Fields(line)[0]
		_, _ = server.Write([]byte("+ Ready\r\n"))
		if n, ok := literalSize(line); ok {
			buf := make([]byte, n+2)
			_, _ = reader.Read(buf)
		}
		_, _ = server.Write([]byte(tag + " OK APPEND completed\r\n"))
	}()
	client := &IMAPClient{
		conn: clientConn,
		r:    bufio.NewReader(clientConn),
		w:    bufio.NewWriter(clientConn),
	}
	defer client.conn.Close()

	result, err := AppendDraft(client, SendRequest{From: "me@example.com", To: []string{"you@example.com"}, Subject: "draft", Text: "body"})
	if err != nil {
		t.Fatal(err)
	}
	if result["folder"] != "Drafts" {
		t.Fatalf("result = %#v", result)
	}
	got := []string{<-commands, <-commands}
	if got[0] != `A0001 LIST "" "*"` {
		t.Fatalf("list command = %q", got[0])
	}
	if !strings.Contains(got[1], `APPEND "Drafts" (\Draft)`) {
		t.Fatalf("append command = %q", got[1])
	}
}

func TestFetchMessageRawUsesBodyPeek(t *testing.T) {
	client, commands := newScriptedIMAPClient(t, []string{
		`* 1 EXISTS`,
		`A0001 OK SELECT completed`,
		`* 1 FETCH (UID 123 RFC822.SIZE 0 BODY[] NIL)`,
		`A0002 OK FETCH completed`,
	})
	defer client.conn.Close()

	if _, err := client.FetchMessage("INBOX", "123", true); err != nil {
		t.Fatal(err)
	}

	want := []string{
		`A0001 SELECT "INBOX"`,
		`A0002 UID FETCH 123 (UID FLAGS INTERNALDATE RFC822.SIZE BODY.PEEK[])`,
	}
	assertCommands(t, commands, want)
}

func TestFetchMessageHeaderOnlyUsesBodyPeekHeader(t *testing.T) {
	client, commands := newScriptedIMAPClient(t, []string{
		`* 1 EXISTS`,
		`A0001 OK SELECT completed`,
		`* 1 FETCH (UID 123 RFC822.SIZE 0 BODY[HEADER] NIL)`,
		`A0002 OK FETCH completed`,
	})
	defer client.conn.Close()

	if _, err := client.FetchMessage("INBOX", "123", false); err != nil {
		t.Fatal(err)
	}

	want := []string{
		`A0001 SELECT "INBOX"`,
		`A0002 UID FETCH 123 (UID FLAGS INTERNALDATE RFC822.SIZE BODY.PEEK[HEADER])`,
	}
	assertCommands(t, commands, want)
}

func TestFetchMessageBodyAndAttachmentsUseBodyPeek(t *testing.T) {
	client, commands := newScriptedIMAPClient(t, []string{
		`* 1 EXISTS`,
		`A0001 OK SELECT completed`,
		`* 1 FETCH (UID 123 RFC822.SIZE 0 BODY[] NIL)`,
		`A0002 OK FETCH completed`,
	})
	defer client.conn.Close()

	if _, err := client.FetchMessageWithOptions("INBOX", "123", FetchOptions{BodyMode: "text", IncludeAttachments: true}); err != nil {
		t.Fatal(err)
	}

	want := []string{
		`A0001 SELECT "INBOX"`,
		`A0002 UID FETCH 123 (UID FLAGS INTERNALDATE RFC822.SIZE BODY.PEEK[])`,
	}
	assertCommands(t, commands, want)
}

func TestFetchAttachmentReturnsContent(t *testing.T) {
	raw := strings.Join([]string{
		"Subject: x",
		"Content-Type: multipart/mixed; boundary=mix",
		"",
		"--mix",
		"Content-Type: text/plain",
		"",
		"body",
		"--mix",
		"Content-Type: text/plain; name=\"note.txt\"",
		"Content-Disposition: attachment; filename=\"note.txt\"",
		"Content-Transfer-Encoding: base64",
		"",
		"aGVsbG8=",
		"--mix--",
		"",
	}, "\r\n")
	client, commands := newScriptedIMAPClient(t, []string{
		`* 1 EXISTS`,
		`A0001 OK SELECT completed`,
		fmt.Sprintf(`* 1 FETCH (UID 123 RFC822.SIZE %d BODY[] {%d}`, len(raw), len(raw)),
		raw,
		`A0002 OK FETCH completed`,
	})
	defer client.conn.Close()

	attachment, err := client.FetchAttachment("INBOX", "123", "1")
	if err != nil {
		t.Fatal(err)
	}
	if attachment.Filename != "note.txt" || attachment.ContentBase64 != "aGVsbG8=" || attachment.Size != 5 {
		t.Fatalf("attachment = %#v", attachment)
	}

	want := []string{
		`A0001 SELECT "INBOX"`,
		`A0002 UID FETCH 123 (UID FLAGS INTERNALDATE RFC822.SIZE BODY.PEEK[])`,
	}
	assertCommands(t, commands, want)
}

func TestMoveFallsBackToCopyStoreUIDExpunge(t *testing.T) {
	client, commands := newScriptedIMAPClient(t, []string{
		`* 1 EXISTS`,
		`A0001 OK SELECT completed`,
		`A0002 BAD MOVE unsupported`,
		`A0003 OK COPY completed`,
		`A0004 OK STORE completed`,
		`A0005 OK UID EXPUNGE completed`,
	})
	defer client.conn.Close()

	if err := client.Move("INBOX", "123", "Archive/2026"); err != nil {
		t.Fatal(err)
	}

	want := []string{
		`A0001 SELECT "INBOX"`,
		`A0002 UID MOVE 123 "Archive/2026"`,
		`A0003 UID COPY 123 "Archive/2026"`,
		`A0004 UID STORE 123 +FLAGS.SILENT (\Deleted)`,
		`A0005 UID EXPUNGE 123`,
	}
	assertCommands(t, commands, want)
}

func TestMoveFallbackFailsWithoutUIDExpunge(t *testing.T) {
	client, commands := newScriptedIMAPClient(t, []string{
		`* 1 EXISTS`,
		`A0001 OK SELECT completed`,
		`A0002 BAD MOVE unsupported`,
		`A0003 OK COPY completed`,
		`A0004 OK STORE completed`,
		`A0005 BAD UID EXPUNGE unsupported`,
	})
	defer client.conn.Close()

	if err := client.Move("INBOX", "123", "Archive/2026"); err == nil {
		t.Fatal("expected move cleanup error when UID EXPUNGE is unsupported")
	}

	want := []string{
		`A0001 SELECT "INBOX"`,
		`A0002 UID MOVE 123 "Archive/2026"`,
		`A0003 UID COPY 123 "Archive/2026"`,
		`A0004 UID STORE 123 +FLAGS.SILENT (\Deleted)`,
		`A0005 UID EXPUNGE 123`,
	}
	assertCommands(t, commands, want)
}

func TestPermanentDeleteRejectsMailboxExpungeFallback(t *testing.T) {
	client, commands := newScriptedIMAPClient(t, []string{
		`* 1 EXISTS`,
		`A0001 OK SELECT completed`,
		`A0002 OK STORE completed`,
		`A0003 BAD UID EXPUNGE unsupported`,
	})
	defer client.conn.Close()

	result, err := client.Delete("INBOX", "123", "Trash", true, false)
	if err == nil {
		t.Fatal("expected permanent delete to fail without UID EXPUNGE")
	}
	if result.OK {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(result.Warning, "mailbox-wide EXPUNGE was not used") {
		t.Fatalf("result = %#v", result)
	}

	want := []string{
		`A0001 SELECT "INBOX"`,
		`A0002 UID STORE 123 +FLAGS.SILENT (\Deleted)`,
		`A0003 UID EXPUNGE 123`,
	}
	assertCommands(t, commands, want)
}

func TestValidateUIDRejectsInjection(t *testing.T) {
	for _, id := range []string{"", "123 456", "123\r\nSTORE", "1;2", "abc", "1,2", "0", "4294967296"} {
		if _, err := validateUID(id); err == nil {
			t.Fatalf("validateUID(%q) expected error", id)
		}
	}
	if got, err := validateUID(" 42 "); err != nil || got != "42" {
		t.Fatalf("validateUID(42) = %q, %v", got, err)
	}
}

func TestParseFetchIgnoresFLAGSInBodyLiteral(t *testing.T) {
	body := "Subject: test\r\n\r\nbody mentions FLAGS (\\Seen) here\r\n"
	msg := parseFetch("INBOX", "55", imapResponse{
		Lines: []string{
			`* 1 FETCH (UID 55 FLAGS (\Flagged) RFC822.SIZE 10 BODY[] {` + strconv.Itoa(len(body)) + `}`,
			`A0001 OK Fetch completed`,
		},
		Literals: []string{body},
	}, FetchOptions{IncludeRaw: true})
	if len(msg.Flags) != 1 || msg.Flags[0] != `\Flagged` {
		t.Fatalf("flags = %#v, want [\\Flagged] from protocol only", msg.Flags)
	}
}

func TestCopyRequiresDestination(t *testing.T) {
	var client *IMAPClient
	if err := client.Copy("INBOX", "123", " "); err == nil {
		t.Fatal("expected destination validation error")
	}
}

func newScriptedIMAPClient(t *testing.T, responseLines []string) (*IMAPClient, <-chan string) {
	t.Helper()
	server, clientConn := net.Pipe()
	commands := make(chan string, len(responseLines))

	go func() {
		defer close(commands)
		defer server.Close()
		reader := bufio.NewReader(server)
		responseIdx := 0
		for responseIdx < len(responseLines) {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			commands <- line
			tag := strings.Fields(line)[0]
			for responseIdx < len(responseLines) {
				response := responseLines[responseIdx]
				responseIdx++
				if strings.HasPrefix(response, "A") {
					response = tag + response[5:]
				}
				_, _ = server.Write([]byte(response + "\r\n"))
				if strings.HasPrefix(response, tag+" ") {
					break
				}
			}
		}
	}()

	return &IMAPClient{
		conn: clientConn,
		r:    bufio.NewReader(clientConn),
		w:    bufio.NewWriter(clientConn),
	}, commands
}

func assertCommands(t *testing.T, commands <-chan string, want []string) {
	t.Helper()
	var got []string
	for len(got) < len(want) {
		cmd, ok := <-commands
		if !ok {
			break
		}
		got = append(got, cmd)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
}

func TestIMAPCommandRejectsLineInjection(t *testing.T) {
	for _, value := range []string{"ALL\r\nB9 CREATE injected", "x\ny", "x\ry", "x\x00y"} {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			client := &IMAPClient{}
			if _, err := client.command("UID SEARCH %s", value); err == nil {
				t.Fatal("accepted injection")
			}
			if _, err := client.commandLiteral("APPEND %s {0}", nil, quoteMailbox(value)); err == nil {
				t.Fatal("accepted literal preamble injection")
			}
			if err := client.CreateFolder(value); err == nil {
				t.Fatal("accepted folder injection")
			}
			if client.tag != 0 {
				t.Fatal("invalid command reached transport")
			}
		})
	}
	for _, flag := range []string{"", `\Seen)`, `\Seen \Deleted`, "x\r\ny"} {
		if err := (&IMAPClient{}).SetFlag("INBOX", "1", flag, true); err == nil {
			t.Fatalf("accepted flag %q", flag)
		}
		if err := (&IMAPClient{}).AppendMessage("Drafts", []string{flag}, time.Time{}, nil); err == nil {
			t.Fatalf("accepted append flag %q", flag)
		}
	}
}

func TestIMAPLoginCancellationAndClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&IMAPClient{}).Login(ctx, "dummy", "dummy"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled login = %v", err)
	}
	for _, mode := range []string{"cancel", "deadline", "close"} {
		t.Run(mode, func(t *testing.T) {
			server, conn := net.Pipe()
			defer server.Close()
			defer conn.Close()
			client := &IMAPClient{conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn)}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			if mode == "close" {
				go func() { done <- client.Close() }()
			} else {
				go func() { done <- client.Login(ctx, "dummy", "dummy") }()
				if mode == "cancel" {
					cancel()
				}
			}
			select {
			case err := <-done:
				if mode != "close" && err == nil {
					t.Fatal("stalled login succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("IMAP operation hung")
			}
		})
	}
}

func TestMailboxEncodingAcrossCommands(t *testing.T) {
	for _, name := range []string{"INBOX", "A&B", "日本語", "Projects/日本語/📮"} {
		if got := decodeModifiedUTF7(encodeModifiedUTF7(name)); got != name {
			t.Fatalf("round trip %q = %q", name, got)
		}
	}
	client, commands := newScriptedIMAPClient(t, []string{
		"A0001 OK", "A0002 OK", "A0003 OK", "A0004 OK", "A0005 OK", "A0006 OK", "A0007 OK", "A0008 OK",
	})
	defer client.conn.Close()
	for _, err := range []error{client.CreateFolder("日本語"), client.RenameFolder("日本語", "A&B"), client.DeleteFolder("日本語"), client.selectFolder("日本語"), client.Copy("日本語", "1", "A&B"), client.Move("日本語", "1", "A&B")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertCommands(t, commands, []string{
		`A0001 CREATE "&ZeVnLIqe-"`, `A0002 RENAME "&ZeVnLIqe-" "A&-B"`, `A0003 DELETE "&ZeVnLIqe-"`, `A0004 SELECT "&ZeVnLIqe-"`,
		`A0005 SELECT "&ZeVnLIqe-"`, `A0006 UID COPY 1 "A&-B"`, `A0007 SELECT "&ZeVnLIqe-"`, `A0008 UID MOVE 1 "A&-B"`,
	})
}

func TestListFoldersAcceptsAtomLiteralAndNilDelimiter(t *testing.T) {
	client, _ := newScriptedIMAPClient(t, []string{
		`* LIST (\HasNoChildren) "/" INBOX`, `* LIST () NIL flat`, `* LIST (\Sent) "/" {12}`, `&ZeVnLIqe- x`, "A0001 OK",
	})
	defer client.conn.Close()
	folders, err := client.ListFolders()
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 3 || folders[0].Name != "INBOX" || folders[0].Delimiter != "/" || folders[1].Delimiter != "" || folders[1].Name != "flat" || folders[2].Name != "日本語 x" {
		t.Fatalf("folders = %#v", folders)
	}
}

func TestFetchSelectsRequestedUIDAndLiteral(t *testing.T) {
	raw := "Subject: target\r\n\r\ntarget body\r\n"
	other := "Subject: other\r\n\r\nother body\r\n"
	client, _ := newScriptedIMAPClient(t, []string{
		"A0001 OK",
		fmt.Sprintf(`* 1 FETCH (BODY[] {%d}`, len(raw)), raw, ` UID 123 FLAGS (\Seen) RFC822.SIZE 44)`,
		fmt.Sprintf(`* 2 FETCH (UID 456 FLAGS (\Flagged) BODY[] {%d}`, len(other)), other, ")",
		`* 3 FETCH (UID 789 FLAGS (\Deleted))`, "A0002 OK",
	})
	defer client.conn.Close()
	msg, err := client.FetchMessageWithOptions("INBOX", "123", FetchOptions{IncludeRaw: true, BodyMode: "text"})
	if err != nil {
		t.Fatal(err)
	}
	if msg.ID != "123" || msg.Raw != raw || msg.Subject != "target" || !strings.Contains(msg.Body, "target body") || msg.Size != 44 || len(msg.Flags) != 1 || msg.Flags[0] != `\Seen` {
		t.Fatalf("message = %#v", msg)
	}
}

func TestFetchMissingUIDReturnsNotFound(t *testing.T) {
	for _, response := range [][]string{{"A0001 OK", "A0002 OK"}, {"A0001 OK", `* 2 FETCH (UID 456 FLAGS ())`, "A0002 OK"}, {"A0001 OK", `* 2 FETCH (UID 999 FLAGS (\Seen))`, "A0002 OK"}} {
		client, _ := newScriptedIMAPClient(t, response)
		_, err := client.FetchMessage("INBOX", "999", false)
		client.conn.Close()
		var remote *output.ExitError
		if !errors.As(err, &remote) || remote.Err.Code != "message_not_found" {
			t.Fatalf("missing fetch error = %v", err)
		}
	}
}

func TestAddressListsPreserveEncodedPunctuation(t *testing.T) {
	for _, raw := range []string{`"Doe, Jane" <jane@example.com>`, `=?UTF-8?Q?Doe=2C_Jan=C3=A9?= <jane@example.com>`, `=?ISO-8859-1?Q?Doe=2C_Jan=E9?= <jane@example.com>`} {
		var summary MessageSummary
		applyHeaders(&summary, netmail.Header{"From": {raw}, "Reply-To": {raw}, "To": {raw}}, false)
		for _, formatted := range []string{summary.From, summary.ReplyTo[0], summary.To[0]} {
			address, err := netmail.ParseAddress(formatted)
			if err != nil || address.Address != "jane@example.com" || !strings.Contains(address.Name, "Doe,") {
				t.Fatalf("address %q = %#v, %v", formatted, address, err)
			}
		}
	}
}

func TestAppendAcceptsUntaggedResponseBeforeContinuation(t *testing.T) {
	server, conn := net.Pipe()
	defer server.Close()
	defer conn.Close()
	client := &IMAPClient{conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn)}
	errCh := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(server)
		line, err := reader.ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		if !strings.Contains(line, `APPEND "&ZeVnLIqe-"`) {
			errCh <- fmt.Errorf("wrong APPEND mailbox: %q", line)
			return
		}
		_, err = io.WriteString(server, "* 2 EXISTS\r\n+ ready\r\n")
		if err != nil {
			errCh <- err
			return
		}
		n, _ := literalSize(strings.TrimSpace(line))
		_, err = io.CopyN(io.Discard, reader, int64(n+2))
		if err == nil {
			_, err = io.WriteString(server, "A0001 OK appended\r\n")
		}
		errCh <- err
	}()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if err := client.AppendMessage("日本語", nil, time.Time{}, []byte("Subject: x\r\n\r\nbody")); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestIMAPRejectsStatusPrefixAndOmitsServerText(t *testing.T) {
	for _, response := range []string{"A0001 NO echoed secret", "A0001 OKAY wrong status"} {
		client, _ := newScriptedIMAPClient(t, []string{response})
		_, err := client.command("NOOP")
		client.conn.Close()
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("status error = %v", err)
		}
	}
	if _, ok := literalSize("* 1 FETCH (BODY[] {-1}"); ok {
		t.Fatal("accepted negative literal")
	}
}

func TestAddressWithoutDisplayNamePreservesQuotedLocalPart(t *testing.T) {
	addresses := splitAddressList(`"last,first"@example.com`)
	if len(addresses) != 1 {
		t.Fatalf("addresses = %#v", addresses)
	}
	parsed, err := netmail.ParseAddress(addresses[0])
	if err != nil || parsed.Address != "last,first@example.com" {
		t.Fatalf("address = %#v, %v", parsed, err)
	}
}

func TestUTF8SearchUsesLiteralAndRejectsRawCriteria(t *testing.T) {
	for _, query := range []string{`SUBJECT "日本語"`, "ALL\r\nB9 CREATE injected"} {
		if _, err := (&IMAPClient{}).Search("INBOX", query); err == nil {
			t.Fatalf("accepted invalid search %q", query)
		}
	}
	for _, filter := range []bool{false, true} {
		t.Run(fmt.Sprint(filter), func(t *testing.T) {
			server, conn := net.Pipe()
			defer server.Close()
			defer conn.Close()
			client := &IMAPClient{conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn)}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			errCh := make(chan error, 1)
			go func() {
				reader := bufio.NewReader(server)
				if _, err := reader.ReadString('\n'); err != nil {
					errCh <- err
					return
				}
				io.WriteString(server, "A0001 OK selected\r\n")
				line, err := reader.ReadString('\n')
				if err != nil {
					errCh <- err
					return
				}
				key := "TEXT"
				if filter {
					key = "UNSEEN FROM"
				}
				if line != "A0002 UID SEARCH CHARSET UTF-8 "+key+" {9}\r\n" {
					errCh <- fmt.Errorf("search command = %q", line)
					return
				}
				io.WriteString(server, "+ continue\r\n")
				literal := make([]byte, 11)
				if _, err := io.ReadFull(reader, literal); err != nil {
					errCh <- err
					return
				}
				if string(literal) != "日本語\r\n" {
					errCh <- fmt.Errorf("literal = %q", literal)
					return
				}
				_, err = io.WriteString(server, "* search\r\nA0002 ok searched\r\n")
				errCh <- err
			}()
			var err error
			if filter {
				_, err = client.ListMessagesWithOptions(MessageListOptions{Folder: "INBOX", From: "日本語", Unread: true})
			} else {
				_, err = client.Search("INBOX", "日本語")
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := <-errCh; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFetchAttributesIgnoreFlagKeywordsAndQuotedValues(t *testing.T) {
	raw := "Subject: real target\r\n\r\n"
	resp := imapResponse{Lines: []string{
		`* 2 FETCH (FLAGS (UID 123 RFC822.SIZE 999 a[b) X-EXT "UID 123" UID 456 RFC822.SIZE 44 BODY[HEADER] {26}`,
		")", "A0001 OK",
	}, Literals: []string{raw}}
	if _, found := requestedFetch(resp, "123"); found {
		t.Fatal("selected UID keyword from FLAGS instead of UID attribute")
	}
	msg := parseFetch("INBOX", "456", resp, FetchOptions{})
	if msg.ID != "456" || msg.Size != 44 || msg.Subject != "real target" || len(msg.Flags) != 5 {
		t.Fatalf("message = %#v", msg)
	}
}
