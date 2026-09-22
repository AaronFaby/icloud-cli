package mail

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/aaronfaby/icloud-cli/internal/output"
)

type PollOptions struct {
	Folder, Cursor string
	Limit          int
	RawHeaders     bool
	StartNow       bool
}
type PollResult struct {
	Messages   []MessageSummary `json:"messages"`
	NextCursor string           `json:"next_cursor"`
	HasMore    bool             `json:"has_more"`
}
type pollCursor struct {
	Version  int    `json:"v"`
	Account  string `json:"a"`
	Folder   string `json:"f"`
	Validity uint64 `json:"u"`
	Next     uint64 `json:"n"`
	Boundary uint64 `json:"b"`
}

var uidStatePattern = regexp.MustCompile(`(?i)^(?:\*|[A-Za-z0-9]+) OK \[(UIDVALIDITY|UIDNEXT) ([0-9]+)\]`)

// PollMessages returns ascending UIDs from a fixed UIDNEXT snapshot. An empty
// cursor starts with existing mail. A completed cursor starts a new snapshot.
func (c *IMAPClient) PollMessages(account string, opts PollOptions) (PollResult, error) {
	result := PollResult{Messages: []MessageSummary{}}
	if opts.StartNow && opts.Cursor != "" {
		return result, output.Validation("invalid_cursor", "start-now cannot be combined with cursor", nil)
	}
	if opts.Limit == 0 {
		opts.Limit = 100
	}
	if opts.Limit < 1 || opts.Limit > 1000 {
		return result, output.Validation("invalid_limit", "poll limit must be between 1 and 1000", nil)
	}
	folder := defaultFolder(opts.Folder)
	accountHash := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(account)))))
	cursor := pollCursor{Version: 1, Account: accountHash, Folder: folder, Next: 1}
	if opts.Cursor != "" {
		if len(opts.Cursor) > 4096 {
			return result, output.Validation("invalid_cursor", "invalid mail cursor", nil)
		}
		raw, err := base64.RawURLEncoding.DecodeString(opts.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || cursor.Account != accountHash || cursor.Folder != folder || cursor.Next < 1 || cursor.Boundary < cursor.Next || cursor.Boundary > 1<<32 || cursor.Validity == 0 {
			return result, output.Validation("invalid_cursor", "cursor is invalid or belongs to another account or folder", nil)
		}
	}
	resp, err := c.command("SELECT %s", quoteMailbox(folder))
	if err != nil {
		return result, pollRemote(err, "imap_select_failed", "failed to select mail folder")
	}
	var validity, next uint64
	for _, line := range resp.Lines {
		for _, m := range uidStatePattern.FindAllStringSubmatch(line, -1) {
			n, e := strconv.ParseUint(m[2], 10, 32)
			if e != nil || n == 0 {
				return result, output.Remote("invalid_mailbox_state", "invalid mailbox UID state", nil)
			}
			if strings.EqualFold(m[1], "UIDVALIDITY") {
				validity = n
			} else {
				next = n
			}
		}
	}
	if validity == 0 || next == 0 {
		return result, output.Remote("missing_mailbox_state", "server did not provide UIDVALIDITY and UIDNEXT", nil)
	}
	if cursor.Validity != 0 && cursor.Validity != validity {
		return result, output.Remote("mailbox_reset", "mailbox UIDVALIDITY changed; discard cursor and restart explicitly", nil)
	}
	cursor.Validity = validity
	if opts.StartNow {
		cursor.Next = next
		cursor.Boundary = next
	}
	if cursor.Boundary == 0 || cursor.Next == cursor.Boundary {
		cursor.Boundary = next
	}
	if next < cursor.Boundary || cursor.Next > cursor.Boundary {
		return result, output.Remote("invalid_mailbox_state", "mailbox UIDNEXT moved backwards", nil)
	}
	if cursor.Next < cursor.Boundary {
		// Use a closed interval and filter returned UIDs: n:* includes the highest
		// message even when n is greater than the highest UID.
		resp, err = c.command("UID SEARCH UID %d:%d", cursor.Next, cursor.Boundary-1)
		if err != nil {
			return result, pollRemote(err, "imap_search_failed", "failed to search mail")
		}
		ids := []uint64{}
		seen := map[uint64]bool{}
		searchFound := false
		for _, line := range resp.Lines {
			fields := strings.Fields(line)
			if len(fields) < 2 || fields[0] != "*" || !strings.EqualFold(fields[1], "SEARCH") {
				continue
			}
			searchFound = true
			for _, id := range fields[2:] {
				n, e := strconv.ParseUint(id, 10, 32)
				if e != nil || n == 0 {
					return result, output.Remote("invalid_search", "invalid search UID", nil)
				}
				if n >= cursor.Next && n < cursor.Boundary && !seen[n] {
					ids = append(ids, n)
					seen[n] = true
				}
			}
		}
		if !searchFound {
			return result, output.Remote("invalid_search", "server omitted SEARCH result", nil)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		result.HasMore = len(ids) > opts.Limit
		if result.HasMore {
			ids = ids[:opts.Limit]
		}
		pageBytes := 0
		for _, uid := range ids {
			id := strconv.FormatUint(uid, 10)
			fetched, e := c.command("UID FETCH %s (UID FLAGS INTERNALDATE RFC822.SIZE BODY.PEEK[HEADER])", id)
			if e != nil {
				return PollResult{}, pollRemote(e, "imap_fetch_failed", "failed to fetch mail message")
			}
			selected, found := requestedFetch(fetched, id)
			if found {
				for _, literal := range selected.Literals {
					pageBytes += len(literal)
				}
				if pageBytes > maxMessageBytes {
					return PollResult{}, output.Remote("poll_page_limit", "poll header page exceeds 32 MiB; retry with a smaller limit", nil)
				}
				msg := parseFetch(folder, id, selected, FetchOptions{RawHeaders: opts.RawHeaders})
				result.Messages = append(result.Messages, msg.MessageSummary)
			}
			cursor.Next = uid + 1
		}
		if !result.HasMore {
			cursor.Next = cursor.Boundary
		}
	}
	// Recheck before returning any cursor: mailbox identity may change during
	// this page (for example following server failover).
	final, err := c.command("SELECT %s", quoteMailbox(folder))
	if err != nil {
		return PollResult{}, pollRemote(err, "imap_select_failed", "failed to select mail folder")
	}
	finalValidity := uint64(0)
	for _, line := range final.Lines {
		for _, m := range uidStatePattern.FindAllStringSubmatch(line, -1) {
			if strings.EqualFold(m[1], "UIDVALIDITY") {
				finalValidity, _ = strconv.ParseUint(m[2], 10, 32)
			}
		}
	}
	if finalValidity != validity {
		return PollResult{}, output.Remote("mailbox_reset", "mailbox UIDVALIDITY changed while polling; discard cursor and restart explicitly", nil)
	}
	raw, _ := json.Marshal(cursor)
	result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	return result, nil
}

// pollRemote maps a protocol failure to exit 4. Context cancellation and an
// existing exit error stay as they are so validation and deadlines do not
// look like an internal fault.
func pollRemote(err error, code, message string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var exit *output.ExitError
	if errors.As(err, &exit) {
		return err
	}
	return output.Remote(code, message, err.Error())
}
