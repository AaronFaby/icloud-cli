package mail

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestIMAPRejectsOversizedLiteralBeforeReadingContent(t *testing.T) {
	for _, size := range []string{fmt.Sprint(maxMessageBytes + 1), "999999999999999999999999999999999999"} {
		client := &IMAPClient{r: bufio.NewReader(strings.NewReader("* 1 FETCH (UID 1 BODY[] {" + size + "}\r\n"))}
		_, err := client.readUntilTag("A1")
		if err == nil || (!strings.Contains(err.Error(), "limit") && !strings.Contains(err.Error(), "size")) {
			t.Fatalf("did not reject declared size before reading missing payload: %v", err)
		}
	}
}

func TestIMAPResponseLineAndCountLimits(t *testing.T) {
	client := &IMAPClient{r: bufio.NewReader(strings.NewReader(strings.Repeat("x", maxIMAPLineBytes+1)))}
	if _, err := client.readUntilTag("A1"); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("oversized line result: %v", err)
	}
	// A large SEARCH result remains valid well beyond the earlier proposed64KiB cap.
	client.r = bufio.NewReader(strings.NewReader("* SEARCH " + strings.Repeat("123456 ", 20000) + "\r\nA1 OK done\r\n"))
	if _, err := client.readUntilTag("A1"); err != nil {
		t.Fatalf("ordinary large inbox response failed: %v", err)
	}
	client.r = bufio.NewReader(strings.NewReader(strings.Repeat("\r\n", maxIMAPResponseLines+1)))
	if _, err := client.readUntilTag("A1"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("response line count result: %v", err)
	}
}

type repeatedByteReader struct{}

func (repeatedByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestIMAPAggregateLiteralLimit(t *testing.T) {
	marker := fmt.Sprintf("* 1 FETCH (BODY[] {%d}\r\n", maxMessageBytes)
	// The second declared literal cannot fit with the first literal + protocol
	// overhead. No second payload is supplied: a size error must precede EOF.
	reader := io.MultiReader(strings.NewReader(marker), io.LimitReader(repeatedByteReader{}, maxMessageBytes), strings.NewReader(")\r\n"+marker))
	client := &IMAPClient{r: bufio.NewReader(reader)}
	_, err := client.readUntilTag("A1")
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("aggregate response was not rejected before second payload: %v", err)
	}
}
