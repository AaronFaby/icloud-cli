<claude-mem-context>
# Memory Context

# [icloud-cli] recent context, 2026-06-09 6:20am PDT

Legend: 🎯session 🔴bugfix 🟣feature 🔄refactor ✅change 🔵discovery ⚖️decision
Format: ID TIME TYPE TITLE
Fetch details: get_observations([IDs]) | Search: mem-search skill

Stats: 1 obs (385t read) | 6,840t work | 94% savings

### Jun 8, 2026
548 4:05p ⚖️ iCloud Go CLI Tool: Project Concept Defined for Agentic AI Use

Access 7k tokens of past work via get_observations([IDs]) or mem-search skill.
</claude-mem-context>

# Agent Guidance

This repo contains a Go CLI named `icloud` for noninteractive, JSON-first iCloud automation.

The v1.0 surface covers:

- Mail over IMAP/SMTP, including folder/message listing, search/get, send with Sent-copy append, move/copy/delete/archive, flags, read state, and batch mutations.
- Calendar discovery and event CRUD over CalDAV.
- Contacts address-book discovery and contact CRUD over CardDAV.
- Capability reporting for unsupported services.

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
GOCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gocache GOMODCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gomodcache go test ./...
```

For local binaries:

```sh
GOCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gocache GOMODCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gomodcache go build -o /private/tmp/icloud-cli ./cmd/icloud
```

When running live tests against iCloud, create disposable test records and clean them up in the same run. For Contacts, use the `contacts books list` entry whose `resource_types` includes `addressbook`; collection roots are not writable address books.

Tags matching `v*` trigger GitHub Actions binary builds for Linux amd64, Linux arm64, and macOS arm64, and publish release assets with sha256 files.

Do not add private iCloud Drive, Notes, Reminders, Photos, or web-session scraping behavior without a new explicit product decision.
