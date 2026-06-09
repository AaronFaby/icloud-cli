package mail

import "testing"

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
	}, false)
	if msg.ID != "183860" {
		t.Fatalf("id = %q", msg.ID)
	}
	if len(msg.Flags) != 2 || msg.Flags[0] != `\Seen` || msg.Flags[1] != `\Flagged` {
		t.Fatalf("flags = %#v", msg.Flags)
	}
}
