package mail

import (
	"encoding/base64"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/aaronfaby/icloud-cli/internal/output"
)

const maxOutgoingAttachmentBytes = 20 << 20

// Resolve explicit local inputs once, so a prepared response sends the bytes previewed.
func resolveAttachments(inputs []SendAttachment) ([]SendAttachment, []AttachmentPreview, error) {
	if len(inputs) > 100 {
		return nil, nil, output.Validation("attachment_limit", "at most 100 attachments are supported", nil)
	}
	resolved := make([]SendAttachment, 0, len(inputs))
	previews := make([]AttachmentPreview, 0, len(inputs))
	total := 0
	for _, a := range inputs {
		if a.Path != "" && a.ContentBase64 != "" {
			return nil, nil, output.Validation("invalid_attachment", "specify path or content_base64, not both", nil)
		}
		var data []byte
		var err error
		if a.Path != "" {
			f, e := os.OpenFile(a.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if e != nil {
				return nil, nil, output.Validation("attachment_read_failed", "cannot open attachment", nil)
			}
			info, e := f.Stat()
			if e != nil || !info.Mode().IsRegular() {
				f.Close()
				return nil, nil, output.Validation("invalid_attachment", "attachment path must be a regular file", nil)
			}
			data, err = io.ReadAll(io.LimitReader(f, int64(maxOutgoingAttachmentBytes-total)+1))
			f.Close()
			if a.Filename == "" {
				a.Filename = filepath.Base(a.Path)
			}
		} else {
			if len(a.ContentBase64) > base64.StdEncoding.EncodedLen(maxOutgoingAttachmentBytes-total) {
				return nil, nil, output.Validation("attachment_limit", "attachments exceed 20 MiB decoded total", nil)
			}
			data, err = base64.StdEncoding.Strict().DecodeString(a.ContentBase64)
		}
		if err != nil {
			return nil, nil, output.Validation("invalid_attachment", "cannot decode or read attachment", nil)
		}
		total += len(data)
		if total > maxOutgoingAttachmentBytes {
			return nil, nil, output.Validation("attachment_limit", "attachments exceed 20 MiB decoded total", nil)
		}
		if a.Filename == "" {
			a.Filename = "attachment"
		}
		a.Filename = filepath.Base(strings.ReplaceAll(a.Filename, "\\", "/"))
		if len(a.Filename) > 255 || strings.ContainsAny(a.Filename, "\r\n\x00") || a.Filename == "." || a.Filename == "/" {
			return nil, nil, output.Validation("invalid_attachment", "invalid attachment filename", nil)
		}
		if len(a.ContentType) > 255 {
			return nil, nil, output.Validation("invalid_attachment", "attachment content_type exceeds 255 bytes", nil)
		}
		if a.ContentType == "" {
			a.ContentType = "application/octet-stream"
		}
		media, params, e := mime.ParseMediaType(a.ContentType)
		if e != nil || strings.HasPrefix(media, "multipart/") || strings.ContainsAny(a.ContentType, "\r\n\x00") {
			return nil, nil, output.Validation("invalid_attachment", "invalid attachment content_type", nil)
		}
		a.ContentType = mime.FormatMediaType(media, params)
		a.Path = ""
		a.ContentBase64 = base64.StdEncoding.EncodeToString(data)
		resolved = append(resolved, a)
		previews = append(previews, AttachmentPreview{Filename: a.Filename, ContentType: a.ContentType, Size: len(data)})
	}
	return resolved, previews, nil
}
