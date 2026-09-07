package mail

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
)

const (
	maxMessageBytes  = 32 << 20
	maxMIMEReadBytes = 128 << 20
	maxMIMEDepth     = 16
	maxMIMEParts     = 1000
)

// Shared across every layer: nested decoders consume the same work allowance.
// A wrapper cannot reset the budget or cause another whole-message allocation.
var errMIMEReadLimit = errors.New("MIME decoded read budget exceeded")

type mimeBudget struct {
	remaining int64
	parts     int
}

type mimeBudgetReader struct {
	reader io.Reader
	budget *mimeBudget
}

func (r mimeBudgetReader) Read(p []byte) (int, error) {
	if r.budget.remaining <= 0 {
		return 0, errMIMEReadLimit
	}
	if int64(len(p)) > r.budget.remaining {
		p = p[:r.budget.remaining]
	}
	n, err := r.reader.Read(p)
	r.budget.remaining -= int64(n)
	if r.budget.remaining < 0 {
		return n, errMIMEReadLimit
	}
	return n, err
}

type messageContent struct {
	Text        string
	HTML        string
	Attachments []Attachment
}

type parsedMIME struct {
	textParts []string
	htmlParts []string
	files     []Attachment
	contentID string
}

func extractReadableText(source Message) (string, error) {
	if strings.TrimSpace(source.Raw) != "" {
		content, err := parseMessageContent(source.Raw)
		if err != nil {
			return "", err
		}
		return content.Text, nil
	}
	return source.Body, nil
}

func parseMIME(raw, contentID string) (parsedMIME, error) {
	parsed := parsedMIME{contentID: contentID}
	if len(raw) > maxMessageBytes {
		return parsed, fmt.Errorf("MIME message exceeds 32 MiB")
	}
	msg, err := netmail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return parsed, fmt.Errorf("invalid MIME message headers")
	}
	budget := &mimeBudget{remaining: maxMIMEReadBytes}
	err = walkMIME(textproto.MIMEHeader(msg.Header), msg.Body, &parsed, budget, 0)
	return parsed, err
}

func parseMessageContent(raw string) (messageContent, error) {
	parsed, err := parseMIME(raw, "")
	if err != nil {
		return messageContent{}, err
	}
	out := messageContent{Attachments: parsed.files}
	for _, text := range parsed.textParts {
		if strings.TrimSpace(text) != "" {
			out.Text = normalizeText(text)
			break
		}
	}
	for _, htmlPart := range parsed.htmlParts {
		if strings.TrimSpace(htmlPart) == "" {
			continue
		}
		var htmlText string
		out.HTML, htmlText, err = sanitizeHTML(htmlPart)
		if err != nil {
			return messageContent{}, err
		}
		if shouldUseHTMLText(out.Text, htmlText) {
			out.Text = htmlText
		}
		break
	}
	return out, nil
}

func shouldUseHTMLText(plainText string, htmlText string) bool {
	if strings.TrimSpace(htmlText) == "" {
		return false
	}
	if strings.TrimSpace(plainText) == "" {
		return true
	}
	plainWords := len(strings.Fields(plainText))
	htmlWords := len(strings.Fields(htmlText))
	return (len(plainText) <= 40 || plainWords <= 5) && htmlWords >= 25 && len(htmlText) >= len(plainText)*4
}

func walkMIME(header textproto.MIMEHeader, body io.Reader, parsed *parsedMIME, budget *mimeBudget, depth int) error {
	if depth > maxMIMEDepth {
		return fmt.Errorf("MIME nesting exceeds 16 levels")
	}
	budget.parts++
	if budget.parts > maxMIMEParts {
		return fmt.Errorf("MIME message exceeds 1000 parts")
	}
	mediaType, params, err := parseMIMEType(header.Get("Content-Type"))
	if err != nil {
		return err
	}
	disposition, dispParams, err := parseMIMEType(header.Get("Content-Disposition"))
	if err != nil {
		return err
	}
	filename := decodedFilename(dispParams["filename"])
	if filename == "" {
		filename = decodedFilename(params["name"])
	}
	isFile := strings.EqualFold(disposition, "attachment") || filename != "" ||
		(mediaType != "" && mediaType != "text/plain" && mediaType != "text/html" && !strings.HasPrefix(mediaType, "multipart/"))
	if mediaType == "" {
		mediaType = "text/plain"
		if isFile {
			mediaType = "application/octet-stream"
		}
	}
	decoded, err := decodeTransfer(body, header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return err
	}
	decoded = mimeBudgetReader{reader: decoded, budget: budget}
	if isFile {
		id := strconv.Itoa(len(parsed.files) + 1)
		var encoded strings.Builder
		var sink io.Writer = io.Discard
		var encoder io.WriteCloser
		if parsed.contentID == id || parsed.contentID == "*" {
			encoder = base64.NewEncoder(base64.StdEncoding, &encoded)
			sink = encoder
		}
		size, err := io.Copy(sink, decoded)
		if err != nil {
			return mimeReadError(err)
		}
		if encoder != nil {
			if err := encoder.Close(); err != nil {
				return err
			}
		}
		file := Attachment{
			ID: id, Filename: filename, ContentType: mediaType, Size: int(size),
			Inline:    strings.EqualFold(disposition, "inline") || (disposition == "" && header.Get("Content-ID") != ""),
			ContentID: strings.Trim(header.Get("Content-ID"), "<>"),
		}
		if parsed.contentID == id || parsed.contentID == "*" {
			file.ContentBase64 = encoded.String()
		}
		parsed.files = append(parsed.files, file)
		return nil
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		if params["boundary"] == "" {
			return fmt.Errorf("multipart MIME body is missing a boundary")
		}
		reader := multipart.NewReader(decoded, params["boundary"])
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return mimeReadError(err)
			}
			if err := walkMIME(part.Header, part, parsed, budget, depth+1); err != nil {
				return err
			}
		}
	}
	if parsed.contentID == "" && (mediaType == "text/plain" || mediaType == "text/html") {
		limit := 0
		if mediaType == "text/html" {
			limit = maxHTMLBytes
		}
		text, err := decodeText(decoded, params["charset"], budget, limit)
		if err != nil {
			return err
		}
		if mediaType == "text/plain" {
			parsed.textParts = append(parsed.textParts, text)
		} else {
			parsed.htmlParts = append(parsed.htmlParts, text)
		}
		return nil
	}
	// Drain unselected text through its decoder so malformed transfers and the
	// aggregate budget remain enforced during attachment-only extraction.
	if _, err := io.Copy(io.Discard, decoded); err != nil {
		return mimeReadError(err)
	}
	return nil
}

func mimeReadError(err error) error {
	if errors.Is(err, errMIMEReadLimit) {
		return errMIMEReadLimit
	}
	// Transfer decoder errors can contain message bytes; expose no body content.
	return fmt.Errorf("invalid MIME transfer encoding or multipart body")
}

func parseMIMEType(value string) (string, map[string]string, error) {
	if value == "" {
		return "", nil, nil
	}
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil {
		return "", nil, fmt.Errorf("invalid MIME content type or disposition")
	}
	return mediaType, params, nil
}

func decodedFilename(filename string) string {
	return decodeHeaderValue(strings.TrimSpace(filename))
}

func decodeTransfer(reader io.Reader, encoding string) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "7bit", "8bit", "binary":
		return reader, nil
	case "quoted-printable":
		return quotedprintable.NewReader(reader), nil
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, reader), nil
	default:
		return nil, fmt.Errorf("unsupported MIME transfer encoding")
	}
}

func decodeText(reader io.Reader, label string, budget *mimeBudget, limit int) (string, error) {
	label = strings.ToLower(strings.TrimSpace(label))
	switch label {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
	default:
		var err error
		reader, err = charset.NewReaderLabel(label, reader)
		if err != nil {
			return "", fmt.Errorf("unsupported MIME charset")
		}
		reader = mimeBudgetReader{reader: reader, budget: budget}
	}
	if limit > 0 {
		reader = io.LimitReader(reader, int64(limit)+1)
	}
	var text strings.Builder
	if _, err := io.Copy(&text, reader); err != nil {
		return "", mimeReadError(err)
	}
	if limit > 0 && text.Len() > limit {
		return "", fmt.Errorf("HTML input exceeds 4 MiB")
	}
	decoded := text.String()
	if !utf8.ValidString(decoded) {
		return "", fmt.Errorf("invalid UTF-8 MIME text")
	}
	if (label == "us-ascii" || label == "ascii") && !isASCII(decoded) {
		return "", fmt.Errorf("invalid ASCII MIME text")
	}
	return decoded, nil
}

func attachmentWithContent(raw string, id string) (Attachment, bool, error) {
	parsed, err := parseMIME(raw, id)
	if err != nil {
		return Attachment{}, false, err
	}
	for _, file := range parsed.files {
		if file.ID == id {
			return file, true, nil
		}
	}
	return Attachment{}, false, nil
}

func normalizeText(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
