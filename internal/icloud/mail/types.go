package mail

type Folder struct {
	Name      string   `json:"name"`
	Delimiter string   `json:"delimiter,omitempty"`
	Flags     []string `json:"flags,omitempty"`
}

type MessageSummary struct {
	ID           string   `json:"id"`
	Folder       string   `json:"folder"`
	Subject      string   `json:"subject,omitempty"`
	RawSubject   string   `json:"raw_subject,omitempty"`
	From         string   `json:"from,omitempty"`
	RawFrom      string   `json:"raw_from,omitempty"`
	To           []string `json:"to,omitempty"`
	RawTo        string   `json:"raw_to,omitempty"`
	CC           []string `json:"cc,omitempty"`
	ReplyTo      []string `json:"reply_to,omitempty"`
	Date         string   `json:"date,omitempty"`
	RawDate      string   `json:"raw_date,omitempty"`
	MessageID    string   `json:"message_id,omitempty"`
	References   string   `json:"references,omitempty"`
	Flags        []string `json:"flags,omitempty"`
	Size         int      `json:"size,omitempty"`
	InternalDate string   `json:"internal_date,omitempty"`
}

type MessageListOptions struct {
	Folder     string
	Limit      int
	Unread     bool
	Flagged    bool
	From       string
	Since      string
	RawHeaders bool
}

type FetchOptions struct {
	IncludeRaw         bool
	RawHeaders         bool
	BodyMode           string
	IncludeAttachments bool
}

type Message struct {
	MessageSummary
	Headers     map[string][]string `json:"headers,omitempty"`
	Body        string              `json:"body,omitempty"`
	HTML        string              `json:"html,omitempty"`
	Raw         string              `json:"raw,omitempty"`
	Attachments []Attachment        `json:"attachments,omitempty"`
}

type Attachment struct {
	ID            string `json:"id"`
	Filename      string `json:"filename,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
	Size          int    `json:"size"`
	Inline        bool   `json:"inline,omitempty"`
	ContentID     string `json:"content_id,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
}

type MutationResult struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Warning string `json:"warning,omitempty"`
}

type SendRequest struct {
	From    string            `json:"from,omitempty"`
	To      []string          `json:"to"`
	CC      []string          `json:"cc,omitempty"`
	BCC     []string          `json:"bcc,omitempty"`
	Subject string            `json:"subject"`
	Text    string            `json:"text,omitempty"`
	HTML    string            `json:"html,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type ResponseInput struct {
	From            string   `json:"from,omitempty"`
	To              []string `json:"to,omitempty"`
	CC              []string `json:"cc,omitempty"`
	BCC             []string `json:"bcc,omitempty"`
	Subject         string   `json:"subject,omitempty"`
	Text            string   `json:"text,omitempty"`
	IncludeOriginal *bool    `json:"include_original,omitempty"`
}

type PreparedResponse struct {
	Action           string            `json:"action"`
	SourceFolder     string            `json:"source_folder"`
	SourceID         string            `json:"source_id"`
	From             string            `json:"from"`
	To               []string          `json:"to,omitempty"`
	CC               []string          `json:"cc,omitempty"`
	BCC              []string          `json:"bcc,omitempty"`
	Subject          string            `json:"subject"`
	Headers          map[string]string `json:"headers,omitempty"`
	IntendedSentCopy bool              `json:"intended_sent_copy"`
	SourceFlag       string            `json:"source_flag,omitempty"`
	Request          SendRequest       `json:"-"`
}

type BatchRequest struct {
	Folder        string   `json:"folder"`
	IDs           []string `json:"ids"`
	ToFolder      string   `json:"to_folder,omitempty"`
	Permanent     bool     `json:"permanent,omitempty"`
	DryRun        bool     `json:"dry_run,omitempty"`
	ArchiveFolder string   `json:"archive_folder,omitempty"`
	TrashFolder   string   `json:"trash_folder,omitempty"`
}
