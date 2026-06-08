package icloud

type ServiceCapability struct {
	Service    string   `json:"service"`
	Status     string   `json:"status"`
	Protocol   string   `json:"protocol,omitempty"`
	Operations []string `json:"operations,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

func Capabilities() []ServiceCapability {
	return []ServiceCapability{
		{
			Service:  "mail",
			Status:   "supported",
			Protocol: "IMAP/SMTP",
			Operations: []string{
				"folders:list", "folders:create", "folders:rename", "folders:delete",
				"messages:list", "messages:get", "messages:search", "messages:send",
				"messages:move", "messages:copy", "messages:delete", "messages:archive",
				"messages:flag", "messages:unflag", "messages:mark-read", "messages:mark-unread",
				"batch:move", "batch:copy", "batch:delete", "batch:flag", "batch:unflag", "batch:mark-read", "batch:mark-unread",
			},
		},
		{
			Service:    "calendar",
			Status:     "supported",
			Protocol:   "CalDAV",
			Operations: []string{"calendars:list", "events:list", "events:create", "events:update", "events:delete"},
		},
		{
			Service:    "contacts",
			Status:     "supported",
			Protocol:   "CardDAV",
			Operations: []string{"books:list", "contacts:list", "contacts:get", "contacts:create", "contacts:update", "contacts:delete"},
		},
		{Service: "icloud_drive", Status: "unsupported", Reason: "No documented app-password protocol for broad personal iCloud Drive manipulation."},
		{Service: "notes", Status: "unsupported", Reason: "No documented app-password protocol for personal Notes manipulation."},
		{Service: "reminders", Status: "unsupported", Reason: "No documented app-password protocol included in V1."},
		{Service: "photos", Status: "unsupported", Reason: "No documented app-password protocol for broad personal Photos manipulation."},
	}
}
