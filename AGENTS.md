<claude-mem-context>
# Memory Context

# [icloud-cli] recent context, 2026-06-15 7:07am PDT

Legend: 🎯session 🔴bugfix 🟣feature 🔄refactor ✅change 🔵discovery ⚖️decision
Format: ID TIME TYPE TITLE
Fetch details: get_observations([IDs]) | Search: mem-search skill

Stats: 5 obs (1,996t read) | 86,767t work | 98% savings

### Jun 8, 2026
548 4:05p ⚖️ iCloud Go CLI Tool: Project Concept Defined for Agentic AI Use
### Jun 12, 2026
555 5:04p 🟣 iCloud CLI v1.0.1 Released to GitHub
556 " ✅ GitHub Actions Workflow Opted Into Node.js 24
### Jun 13, 2026
557 8:24a ⚖️ iCloud CLI: Logging System Design — Environment-Variable-Driven with File Rotation
### Jun 14, 2026
558 8:03a ⚖️ iCloud CLI: 5 Planned Improvements Scoped for Next Release

Access 87k tokens of past work via get_observations([IDs]) or mem-search skill.
</claude-mem-context>

# Agent Guidance

This repo contains a Go CLI named `icloud` for noninteractive, JSON-first iCloud automation. The tool is optimized for agentic use, container portability, documented-protocol access, and a small auditable supply-chain surface; the current Go module intentionally has no third-party module dependencies.

The v1.0 surface covers:

- Mail over IMAP/SMTP, including folder/message listing, search/get, send with Sent-copy append, text-threaded reply/reply-all/forward, Drafts append, move/copy/delete/archive, flags, read state, and batch mutations.
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

Logging is environment-configured with safe defaults:

- `ICLOUD_CLI_LOG=file|stderr|off` defaults to `file`.
- `ICLOUD_CLI_LOG_LEVEL=info|warn|error` defaults to `warn`.
- `ICLOUD_CLI_LOG_FILE` defaults to the OS cache location under `icloud-cli/icloud.log`.
- `ICLOUD_CLI_LOG_SIZE` defaults to `10` MB and `ICLOUD_CLI_LOG_NUM` defaults to `3`.

Use `icloud log status` to inspect the effective logging configuration. Logs should be detailed operational metadata only; never log app passwords, auth headers, SMTP auth payloads, Apple ID values, message bodies, raw RFC822, vCards, iCalendar payloads, mail subjects, event summaries, or contact names.

CLI output is JSON by default, and `--json` is accepted on every command as a no-op for automation consistency. Nested `--help` should return a successful JSON help envelope and exit 0 without requiring credentials or network access.

Mail message summaries decode encoded headers by default. `icloud mail messages list` supports first-class triage filters such as `--unread`, `--since 24h`, `--from domain.com`, `--flagged`, and `--limit`; use `--raw-headers` when raw subject/from/to/date fields are needed. Reply, reply-all, and forward preserve text-threading headers for replies, use `Fwd:` subject handling for forwards, support `--dry-run` metadata previews, and support `--draft` Drafts append. Actual reply/reply-all/forward sends must continue to append a Sent copy. Calendar event listing supports either `--calendar` hrefs or `--calendar-name` display-name lookup.

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
