package mail

import (
	"fmt"
	"net/textproto"
	"runtime"
	"strings"
	"testing"
)

func nestedMIME(depth, payloadBytes int) string {
	var raw strings.Builder
	raw.Grow(payloadBytes + depth*160)
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&raw, "Content-Type: multipart/mixed; boundary=level-%d\r\n\r\n--level-%d\r\n", i, i)
	}
	raw.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	raw.WriteString(strings.Repeat("x", payloadBytes))
	for i := depth - 1; i >= 0; i-- {
		fmt.Fprintf(&raw, "\r\n--level-%d--\r\n", i)
	}
	return raw.String()
}

func TestNestedMIMEAbortsBeforeMessageSizedCopies(t *testing.T) {
	raw := nestedMIME(64, 4<<20)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := parseMessageContent(raw)
	runtime.ReadMemStats(&after)
	if err == nil || !strings.Contains(err.Error(), "nesting") {
		t.Fatalf("nested message was not rejected: %v", err)
	}
	// The old walker allocated over 600 MiB. Streaming must reject from the
	// headers, before reading/copying the 4 MiB leaf at any nesting layer.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("nested MIME allocated %d bytes before rejection", allocated)
	} else {
		t.Logf("nested MIME rejected after allocating %d bytes", allocated)
	}
}

func TestMIMEReadBudgetIsSharedAcrossNestedParts(t *testing.T) {
	raw := "--a\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\n" + strings.Repeat("x", 512) + "\r\n--b--\r\n--a--\r\n"
	parsed := parsedMIME{}
	budget := &mimeBudget{remaining: 1024}
	err := walkMIME(textproto.MIMEHeader{"Content-Type": {"multipart/mixed; boundary=a"}}, strings.NewReader(raw), &parsed, budget, 0)
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("nested reads reset the shared budget: remaining=%d err=%v", budget.remaining, err)
	}
	if _, err := parseMessageContent(nestedMIME(3, 4<<20)); err != nil {
		t.Fatalf("ordinary 4 MiB nested message failed: %v", err)
	}
}

func TestMIMEMessageSizeAndPartCountLimits(t *testing.T) {
	if _, err := parseMessageContent(strings.Repeat("x", maxMessageBytes+1)); err == nil || !strings.Contains(err.Error(), "32 MiB") {
		t.Fatalf("oversized message result: %v", err)
	}
	var raw strings.Builder
	raw.WriteString("Content-Type: multipart/mixed; boundary=a\r\n\r\n")
	for i := 0; i < maxMIMEParts; i++ {
		raw.WriteString("--a\r\nContent-Type: text/plain\r\n\r\nx\r\n")
	}
	raw.WriteString("--a--\r\n")
	if _, err := parseMessageContent(raw.String()); err == nil || !strings.Contains(err.Error(), "1000 parts") {
		t.Fatalf("excessive MIME part count result: %v", err)
	}
}
