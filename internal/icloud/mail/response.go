package mail

import (
	netmail "net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/aaronfaby/icloud-cli/internal/config"
	"github.com/aaronfaby/icloud-cli/internal/output"
)

type ResponseKind string

const (
	ResponseReply    ResponseKind = "reply"
	ResponseReplyAll ResponseKind = "reply-all"
	ResponseForward  ResponseKind = "forward"
)

func PrepareResponse(cfg config.Config, source Message, kind ResponseKind, input ResponseInput, action string) (PreparedResponse, error) {
	if strings.TrimSpace(input.Text) == "" {
		return PreparedResponse{}, output.Validation("missing_response_text", "response text is required", nil)
	}
	from := strings.TrimSpace(input.From)
	if from == "" {
		from = cfg.AppleID
	}
	if from == "" {
		return PreparedResponse{}, output.Validation("missing_from", "from address is required", nil)
	}
	includeOriginal := true
	if input.IncludeOriginal != nil {
		includeOriginal = *input.IncludeOriginal
	}
	subject := strings.TrimSpace(input.Subject)
	if subject == "" {
		subject = defaultResponseSubject(kind, source.Subject)
	}
	headers := responseHeaders(kind, source)
	if _, ok := headers["Message-ID"]; !ok {
		headers["Message-ID"] = generateMessageID(from)
	}
	body := strings.TrimRight(input.Text, "\r\n")
	if includeOriginal {
		body = body + "\n\n" + originalBlock(kind, source)
	}
	to, cc, err := responseRecipients(kind, source, input, from, cfg.AppleID)
	if err != nil {
		return PreparedResponse{}, err
	}
	req := SendRequest{
		From:    from,
		To:      to,
		CC:      cc,
		BCC:     uniqueAddresses(input.BCC, nil),
		Subject: subject,
		Text:    body,
		Headers: headers,
	}
	sourceFlag := ""
	switch kind {
	case ResponseReply, ResponseReplyAll:
		sourceFlag = `\Answered`
	case ResponseForward:
		sourceFlag = `$Forwarded`
	}
	return PreparedResponse{
		Action:           action,
		SourceFolder:     source.Folder,
		SourceID:         source.ID,
		From:             req.From,
		To:               req.To,
		CC:               req.CC,
		BCC:              req.BCC,
		Subject:          req.Subject,
		Headers:          req.Headers,
		IntendedSentCopy: action == "send",
		SourceFlag:       sourceFlag,
		Request:          req,
	}, nil
}

func responseRecipients(kind ResponseKind, source Message, input ResponseInput, from string, appleID string) ([]string, []string, error) {
	exclude := map[string]bool{}
	for _, addr := range []string{from, appleID} {
		if key := addressKey(addr); key != "" {
			exclude[key] = true
		}
	}
	switch kind {
	case ResponseReply:
		to := firstNonEmptyAddressList(source.ReplyTo, []string{source.From})
		to = uniqueAddresses(to, exclude)
		if len(to) == 0 {
			return nil, nil, output.Validation("missing_reply_recipient", "source message has no reply recipient", nil)
		}
		return to, uniqueAddresses(input.CC, exclude), nil
	case ResponseReplyAll:
		to := append([]string{}, firstNonEmptyAddressList(source.ReplyTo, []string{source.From})...)
		to = append(to, source.To...)
		to = uniqueAddresses(to, exclude)
		ccExclude := cloneAddressSet(exclude)
		for _, addr := range to {
			if key := addressKey(addr); key != "" {
				ccExclude[key] = true
			}
		}
		cc := uniqueAddresses(append(append([]string{}, source.CC...), input.CC...), ccExclude)
		if len(to) == 0 && len(cc) == 0 {
			return nil, nil, output.Validation("missing_reply_recipient", "source message has no reply-all recipient", nil)
		}
		return to, cc, nil
	case ResponseForward:
		to := uniqueAddresses(input.To, nil)
		if len(to) == 0 {
			return nil, nil, output.Validation("missing_forward_recipient", "forward requires at least one to recipient", nil)
		}
		ccExclude := cloneAddressSet(exclude)
		for _, addr := range to {
			if key := addressKey(addr); key != "" {
				ccExclude[key] = true
			}
		}
		return to, uniqueAddresses(input.CC, ccExclude), nil
	default:
		return nil, nil, output.Validation("unknown_response_kind", "unknown response command", string(kind))
	}
}

func responseHeaders(kind ResponseKind, source Message) map[string]string {
	headers := map[string]string{}
	if kind == ResponseReply || kind == ResponseReplyAll {
		if strings.TrimSpace(source.MessageID) != "" {
			headers["In-Reply-To"] = strings.TrimSpace(source.MessageID)
			headers["References"] = buildReferences(source.References, source.MessageID)
		}
	}
	return headers
}

func defaultResponseSubject(kind ResponseKind, subject string) string {
	subject = strings.TrimSpace(subject)
	switch kind {
	case ResponseForward:
		if hasSubjectPrefix(subject, "fwd") || hasSubjectPrefix(subject, "fw") {
			return subject
		}
		return "Fwd: " + subject
	default:
		if hasSubjectPrefix(subject, "re") {
			return subject
		}
		return "Re: " + subject
	}
}

func hasSubjectPrefix(subject, prefix string) bool {
	re := regexp.MustCompile(`(?i)^\s*` + regexp.QuoteMeta(prefix) + `\s*:`)
	return re.MatchString(subject)
}

func buildReferences(existing, messageID string) string {
	existing = strings.TrimSpace(existing)
	messageID = strings.TrimSpace(messageID)
	if existing == "" {
		return messageID
	}
	if messageID == "" || strings.Contains(existing, messageID) {
		return existing
	}
	return existing + " " + messageID
}

func originalBlock(kind ResponseKind, source Message) string {
	text := strings.TrimRight(extractReadableText(source), "\r\n")
	switch kind {
	case ResponseForward:
		lines := []string{
			"---------- Forwarded message ---------",
			"From: " + source.From,
			"Date: " + source.Date,
			"Subject: " + source.Subject,
			"To: " + strings.Join(source.To, ", "),
		}
		if len(source.CC) > 0 {
			lines = append(lines, "Cc: "+strings.Join(source.CC, ", "))
		}
		lines = append(lines, "", quoteOriginal(text))
		return strings.Join(lines, "\n")
	default:
		intro := "On " + source.Date + ", " + source.From + " wrote:"
		return intro + "\n" + quoteOriginal(text)
	}
}

func quoteOriginal(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + line
		}
	}
	return strings.Join(lines, "\n")
}

func uniqueAddresses(values []string, exclude map[string]bool) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, addr := range expandAddresses(value) {
			key := addressKey(addr)
			if key == "" || seen[key] || exclude[key] {
				continue
			}
			seen[key] = true
			out = append(out, addr)
		}
	}
	return out
}

func expandAddresses(value string) []string {
	addrs, err := netmail.ParseAddressList(value)
	if err != nil {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		return []string{value}
	}
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, formatAddress(addr))
	}
	return out
}

func addressKey(value string) string {
	addr, err := netmail.ParseAddress(value)
	if err == nil {
		return strings.ToLower(addr.Address)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToLower(value)
}

func cloneAddressSet(in map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstNonEmptyAddressList(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func generateMessageID(from string) string {
	domain := "icloud-cli.local"
	if addr, err := netmail.ParseAddress(from); err == nil {
		if idx := strings.LastIndex(addr.Address, "@"); idx >= 0 && idx < len(addr.Address)-1 {
			domain = addr.Address[idx+1:]
		}
	}
	return "<" + time.Now().UTC().Format("20060102150405.000000000") + ".icloud-cli@" + domain + ">"
}
