package mail

import (
	"bytes"
	"encoding/base64"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
)

type messageContent struct {
	Text        string
	HTML        string
	Attachments []Attachment
}

type parsedMIME struct {
	textParts []string
	htmlParts []string
	files     []Attachment
}

func extractReadableText(source Message) string {
	if strings.TrimSpace(source.Raw) != "" {
		if content, err := parseMessageContent(source.Raw); err == nil && strings.TrimSpace(content.Text) != "" {
			return content.Text
		}
	}
	return source.Body
}

func parseMessageContent(raw string) (messageContent, error) {
	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return messageContent{}, err
	}
	body, _ := io.ReadAll(msg.Body)
	parsed := parsedMIME{}
	walkMIME(textproto.MIMEHeader(msg.Header), body, &parsed)

	out := messageContent{Attachments: parsed.files}
	for _, text := range parsed.textParts {
		if strings.TrimSpace(text) != "" {
			out.Text = text
			break
		}
	}
	for _, htmlPart := range parsed.htmlParts {
		if strings.TrimSpace(htmlPart) == "" {
			continue
		}
		out.HTML = sanitizeHTML(htmlPart)
		if out.Text == "" {
			out.Text = htmlToText(htmlPart)
		}
		break
	}
	return out, nil
}

func walkMIME(header textproto.MIMEHeader, body []byte, parsed *parsedMIME) {
	mediaType, params, _ := mime.ParseMediaType(header.Get("Content-Type"))
	disposition, dispParams, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	filename := decodedFilename(dispParams["filename"])
	if filename == "" {
		filename = decodedFilename(params["name"])
	}

	lowerType := strings.ToLower(mediaType)
	if lowerType == "" {
		if filename != "" || strings.EqualFold(disposition, "attachment") {
			lowerType = "application/octet-stream"
		} else {
			lowerType = "text/plain"
		}
	}

	if strings.HasPrefix(lowerType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		reader := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			partBody, _ := io.ReadAll(part)
			walkMIME(part.Header, partBody, parsed)
		}
		return
	}

	decoded := decodeTransfer(body, header.Get("Content-Transfer-Encoding"))
	isFile := strings.EqualFold(disposition, "attachment") || filename != ""
	if isFile {
		parsed.files = append(parsed.files, Attachment{
			ID:          strconv.Itoa(len(parsed.files) + 1),
			Filename:    filename,
			ContentType: lowerType,
			Size:        len(decoded),
			Inline:      strings.EqualFold(disposition, "inline"),
			ContentID:   strings.Trim(header.Get("Content-ID"), "<>"),
		})
		return
	}

	switch lowerType {
	case "text/plain":
		parsed.textParts = append(parsed.textParts, string(decoded))
	case "text/html":
		parsed.htmlParts = append(parsed.htmlParts, string(decoded))
	}
}

func decodedFilename(filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return ""
	}
	return decodeHeaderValue(filename)
}

func decodeTransfer(body []byte, encoding string) []byte {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
		if err == nil {
			return decoded
		}
	case "base64":
		decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(bytes.TrimSpace(body))))
		if err == nil {
			return decoded
		}
	}
	return body
}

func attachmentWithContent(raw string, id string) (Attachment, bool, error) {
	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return Attachment{}, false, err
	}
	body, _ := io.ReadAll(msg.Body)
	var found Attachment
	var index int
	ok := findAttachmentWithContent(textproto.MIMEHeader(msg.Header), body, id, &index, &found)
	return found, ok, nil
}

func findAttachmentWithContent(header textproto.MIMEHeader, body []byte, id string, index *int, found *Attachment) bool {
	mediaType, params, _ := mime.ParseMediaType(header.Get("Content-Type"))
	disposition, dispParams, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	filename := decodedFilename(dispParams["filename"])
	if filename == "" {
		filename = decodedFilename(params["name"])
	}
	lowerType := strings.ToLower(mediaType)

	if strings.HasPrefix(lowerType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return false
		}
		reader := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				return false
			}
			if err != nil {
				return false
			}
			partBody, _ := io.ReadAll(part)
			if findAttachmentWithContent(part.Header, partBody, id, index, found) {
				return true
			}
		}
	}

	decoded := decodeTransfer(body, header.Get("Content-Transfer-Encoding"))
	if lowerType == "" {
		lowerType = "application/octet-stream"
	}
	if strings.EqualFold(disposition, "attachment") || filename != "" {
		(*index)++
		attachmentID := strconv.Itoa(*index)
		if attachmentID == id {
			*found = Attachment{
				ID:            attachmentID,
				Filename:      filename,
				ContentType:   lowerType,
				Size:          len(decoded),
				Inline:        strings.EqualFold(disposition, "inline"),
				ContentID:     strings.Trim(header.Get("Content-ID"), "<>"),
				ContentBase64: base64.StdEncoding.EncodeToString(decoded),
			}
			return true
		}
	}
	return false
}

func sanitizeHTML(input string) string {
	out := regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`).ReplaceAllString(input, "")
	out = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`).ReplaceAllString(out, "")
	out = regexp.MustCompile(`(?is)\s+on[a-z0-9_-]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`).ReplaceAllString(out, "")
	out = regexp.MustCompile(`(?is)\s+(href|src)\s*=\s*("[^"]*(javascript|data):[^"]*"|'[^']*(javascript|data):[^']*'|[^\s>]*(javascript|data):[^\s>]*)`).ReplaceAllString(out, "")
	return strings.TrimSpace(out)
}

func htmlToText(input string) string {
	out := regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`).ReplaceAllString(input, "")
	out = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`).ReplaceAllString(out, "")
	out = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/li|/tr)\b[^>]*>`).ReplaceAllString(out, "\n")
	out = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(out, " ")
	out = html.UnescapeString(out)
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
