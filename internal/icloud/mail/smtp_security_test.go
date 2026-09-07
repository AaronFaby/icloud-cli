package mail

import (
	"context"
	"errors"
	"fmt"
	"github.com/aaronfaby/icloud-cli/internal/output"
	"io"
	"net"
	netmail "net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func TestSMTPRejectsHeaderNameInjection(t *testing.T) {
	for _, name := range []string{"X-Test: ok\r\nBcc", "X\nInjected", "", "X Bad", "X\x00Bad", "X-é", "Subject:"} {
		_, err := buildMessage(SendRequest{From: "sender@example.com", To: []string{"to@example.com"}, Headers: map[string]string{name: "value"}})
		if err == nil {
			t.Errorf("accepted invalid header name %q", name)
		}
	}
}

func TestSMTPUnicodeAddressHeadersRoundTrip(t *testing.T) {
	raw, err := buildMessage(SendRequest{From: "José <jose@example.com>", To: []string{`"Doe, Jane" <jane@example.com>`, "日本語 <jp@example.com>"}, CC: []string{"Zoë <zoe@example.com>"}, Headers: map[string]string{"Reply-To": "René <rene@example.com>"}})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := netmail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for key, names := range map[string][]string{"From": {"José"}, "To": {"Doe, Jane", "日本語"}, "Cc": {"Zoë"}, "Reply-To": {"René"}} {
		addrs, err := msg.Header.AddressList(key)
		if err != nil || len(addrs) != len(names) {
			t.Fatalf("%s=%q err=%v", key, msg.Header.Get(key), err)
		}
		for i, name := range names {
			if addrs[i].Name != name || !strings.Contains(addrs[i].Address, "@") {
				t.Fatalf("%s address=%#v", key, addrs[i])
			}
		}
	}
	if got, err := envelopeAddress(`"local space"@example.com`); err != nil || got != `"local space"@example.com` {
		t.Fatalf("quoted local-part=%q err=%v", got, err)
	}
	for _, bad := range []string{"a\x00b@example.com", "a\r\nb@example.com", "é@example.com"} {
		if _, err := envelopeAddress(bad); err == nil {
			t.Fatalf("accepted invalid/unsupported envelope %q", bad)
		}
	}
}

func TestSMTPContextBoundsGreetingAndCommands(t *testing.T) {
	for _, stage := range []string{"greeting", "ehlo", "tls"} {
		t.Run(stage, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				if stage != "greeting" {
					_, _ = fmt.Fprint(conn, "220 local test\r\n")
					tc := textproto.NewConn(conn)
					_, _ = tc.ReadLine()
					if stage == "tls" {
						_, _ = fmt.Fprint(conn, "250 STARTTLS\r\n")
						_, _ = tc.ReadLine()
						_, _ = fmt.Fprint(conn, "220 start TLS\r\n")
					}
				}
				_, _ = io.Copy(io.Discard, conn)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
			defer cancel()
			start := time.Now()
			err = sendMailTLSContext(ctx, listener.Addr().String(), "dummy", "dummy", "a@example.com", []string{"b@example.com"}, []byte("body"))
			if err == nil || time.Since(start) > time.Second {
				t.Fatalf("stage=%s err=%v duration=%s", stage, err, time.Since(start))
			}
			<-done
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sendMailTLSContext(ctx, "127.0.0.1:1", "", "", "", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context error=%v", err)
	}
}

func TestSMTPAcceptedDataRemainsSuccessWhenQuitStalls(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = fmt.Fprint(server, "250 queued\r\n")
		_, _ = textproto.NewConn(server).ReadLine() // Accept QUIT but never reply.
	}()
	start := time.Now()
	if err := finishSMTPSend(textproto.NewConn(client), client, time.Now().Add(50*time.Millisecond)); err != nil {
		t.Fatalf("accepted DATA incorrectly reported as failed: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("QUIT was not bounded")
	}
	<-done
}

func TestSMTPAuthenticationErrorIsTypedAndRedacted(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	_ = client.SetDeadline(time.Now().Add(time.Second))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = textproto.NewConn(server).ReadLine()
		_, _ = fmt.Fprint(server, "535 echoed-secret-auth-payload\r\n")
	}()
	err := smtpAuthenticate(textproto.NewConn(client), "dummy", "dummy-secret")
	var typed *output.ExitError
	if !errors.As(err, &typed) || typed.ExitCode != output.ExitAuth || strings.Contains(fmt.Sprint(typed), "echoed-secret") || strings.Contains(fmt.Sprint(typed.Err.Detail), "echoed-secret") {
		t.Fatalf("authentication result=%#v", err)
	}
	<-done
}

func TestSMTPRecipientSuccessAndRejectionStatuses(t *testing.T) {
	for _, code := range []int{250, 251, 252, 450, 550} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			_ = client.SetDeadline(time.Now().Add(time.Second))
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = textproto.NewConn(server).ReadLine()
				_, _ = fmt.Fprintf(server, "%d recipient status\r\n", code)
			}()
			err := smtpRecipient(textproto.NewConn(client), "to@example.com")
			if (err == nil) != (code < 300) {
				t.Fatalf("code=%d err=%v", code, err)
			}
			<-done
		})
	}
}

func TestSMTPHeaderPhysicalLineLimit(t *testing.T) {
	base := SendRequest{From: "sender@example.com", To: []string{"to@example.com"}}
	for _, tc := range []struct {
		name    string
		request SendRequest
	}{
		{"ASCII subject", SendRequest{Subject: strings.Repeat("long subject ", 100)}},
		{"encoded subject", SendRequest{Subject: strings.Repeat("日本語", 100)}},
		{"recipient list", SendRequest{To: strings.Fields(strings.Repeat("long-recipient@example.com ", 50))}},
		{"custom value", SendRequest{Headers: map[string]string{"X-Long": strings.Repeat("a", 998)}}},
		{"custom name", SendRequest{Headers: map[string]string{strings.Repeat("X", 998): "a"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.request
			req.From = base.From
			if len(req.To) == 0 {
				req.To = base.To
			}
			raw, err := buildMessage(req)
			var typed *output.ExitError
			if !errors.As(err, &typed) || typed.ExitCode != output.ExitValidation || typed.Err.Code != "header_too_long" || len(raw) != 0 {
				t.Fatalf("oversized header accepted or incorrect error: bytes=%d err=%#v", len(raw), err)
			}
		})
	}
	base.Subject = strings.Repeat("a", 998-len("Subject: "))
	raw, err := buildMessage(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.SplitN(string(raw), "\r\n\r\n", 2)[0], "\r\n") {
		if len(line) > 998 {
			t.Fatalf("oversized physical header: %d octets", len(line))
		}
	}
}
