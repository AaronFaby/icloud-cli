package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestPollHelpAndValidation(t *testing.T) {
	for _, args := range [][]string{{"mail", "messages", "poll", "--help"}, {"calendar", "events", "--help"}, {"contacts", "contacts", "--help"}} {
		var out, stderr bytes.Buffer
		if code := Run(args, strings.NewReader(""), &out, &stderr); code != 0 {
			t.Fatalf("help failed: %s %s", out.String(), stderr.String())
		}
	}
}

func TestPollNormalizeAndRejectInvalidOptions(t *testing.T) {
	got := normalizeArgs([]string{"mail", "messages", "poll", "--start-now", "--json", "--limit", "1"})
	if strings.Join(got, " ") != "mail messages poll --start-now --limit 1" {
		t.Fatalf("%v", got)
	}
	for _, tail := range [][]string{{"--limit", "0"}, {"--start-now", "--cursor", "secret"}, {"unexpected"}} {
		args := append([]string{"mail", "messages", "poll"}, tail...)
		var out, stderr bytes.Buffer
		if code := Run(args, strings.NewReader(""), &out, &stderr); code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}
