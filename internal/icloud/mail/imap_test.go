package mail

import (
	"bufio"
	"net"
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
	msg := parseFetch("INBOX", "183860", imapResponse{
		Lines: []string{
			`* 1 FETCH (UID 183860 RFC822.SIZE 42 BODY[HEADER] {10}`,
			"Subject: x",
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
		Literals: []string{"Subject: =?utf-8?B?5pel5pys6Kqe?=\r\nFrom: =?utf-8?B?5bed5LiK?= <sender@example.com>\r\nTo: =?utf-8?B?5Y+X5L+h6ICF?= <to@example.com>\r\nDate: Sun, 14 Jun 2026 10:00:00 -0700\r\n\r\n"},
	}, FetchOptions{RawHeaders: true})
	if msg.Subject != "日本語" || msg.RawSubject != "=?utf-8?B?5pel5pys6Kqe?=" {
		t.Fatalf("subject = %q raw = %q", msg.Subject, msg.RawSubject)
	}
	if msg.From != "川上 <sender@example.com>" || msg.RawFrom == "" {
		t.Fatalf("from = %q raw = %q", msg.From, msg.RawFrom)
	}
	if len(msg.To) != 1 || msg.To[0] != "受信者 <to@example.com>" || msg.RawTo == "" || msg.RawDate == "" {
		t.Fatalf("to/raw = %#v raw_to=%q raw_date=%q", msg.To, msg.RawTo, msg.RawDate)
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

func TestMoveFallsBackToCopyStoreExpunge(t *testing.T) {
	client, commands := newScriptedIMAPClient(t, []string{
		`* 1 EXISTS`,
		`A0001 OK SELECT completed`,
		`A0002 BAD MOVE unsupported`,
		`A0003 OK COPY completed`,
		`A0004 OK STORE completed`,
		`A0005 OK EXPUNGE completed`,
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
		`A0005 EXPUNGE`,
	}
	assertCommands(t, commands, want)
}

func TestPermanentDeleteFallsBackToExpunge(t *testing.T) {
	client, commands := newScriptedIMAPClient(t, []string{
		`* 1 EXISTS`,
		`A0001 OK SELECT completed`,
		`A0002 OK STORE completed`,
		`A0003 BAD UID EXPUNGE unsupported`,
		`A0004 OK EXPUNGE completed`,
	})
	defer client.conn.Close()

	result, err := client.Delete("INBOX", "123", "Trash", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || !strings.Contains(result.Warning, "EXPUNGE fallback") {
		t.Fatalf("result = %#v", result)
	}

	want := []string{
		`A0001 SELECT "INBOX"`,
		`A0002 UID STORE 123 +FLAGS.SILENT (\Deleted)`,
		`A0003 UID EXPUNGE 123`,
		`A0004 EXPUNGE`,
	}
	assertCommands(t, commands, want)
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
