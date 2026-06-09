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
