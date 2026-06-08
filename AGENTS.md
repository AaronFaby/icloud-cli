<claude-mem-context>
# Memory Context

# [icloud-cli] recent context, 2026-06-08 4:04pm PDT

No previous sessions found.
</claude-mem-context>

# Agent Guidance

This repo contains a Go CLI named `icloud` for noninteractive, JSON-first iCloud automation.

Use documented protocols only unless the user explicitly approves private iCloud API work:

- Mail: IMAP/SMTP.
- Calendar: CalDAV.
- Contacts: CardDAV.

Credential precedence is environment first, then config file:

- `ICLOUD_APPLE_ID`
- `ICLOUD_APP_PASSWORD`
- `ICLOUD_CONFIG`

The config file can contain plaintext credentials only when the user explicitly runs `icloud auth save`.

When testing in this sandbox, keep Go's build cache inside the workspace:

```sh
GOCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gocache go test ./...
```

Do not add private iCloud Drive, Notes, Reminders, Photos, or web-session scraping behavior without a new explicit product decision.
