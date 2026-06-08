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
	From         string   `json:"from,omitempty"`
	To           []string `json:"to,omitempty"`
	Date         string   `json:"date,omitempty"`
	MessageID    string   `json:"message_id,omitempty"`
	Flags        []string `json:"flags,omitempty"`
	Size         int      `json:"size,omitempty"`
	InternalDate string   `json:"internal_date,omitempty"`
}

type Message struct {
	MessageSummary
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
	Raw     string              `json:"raw,omitempty"`
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

type BatchRequest struct {
	Folder        string   `json:"folder"`
	IDs           []string `json:"ids"`
	ToFolder      string   `json:"to_folder,omitempty"`
	Permanent     bool     `json:"permanent,omitempty"`
	DryRun        bool     `json:"dry_run,omitempty"`
	ArchiveFolder string   `json:"archive_folder,omitempty"`
	TrashFolder   string   `json:"trash_folder,omitempty"`
}
