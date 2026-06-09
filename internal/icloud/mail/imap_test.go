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
