# iCloud CLI

`icloud` is a Go CLI for agentic access to Apple iCloud services through documented, app-password-compatible protocols. It is built for automation environments where predictable JSON, noninteractive auth, and a small portable binary matter more than a graphical client experience.

## Purpose

This project exists to give agents, scripts, and containerized jobs a narrow, inspectable way to work with iCloud data without browser automation or private iCloud web sessions. The design goals are:

- Agentic use: every command is noninteractive, JSON-first, and suitable for tool-calling loops that need stable output envelopes and exit codes.
- Documented protocols: Mail uses IMAP/SMTP, Calendar uses CalDAV, and Contacts uses CardDAV. Unsupported services return structured explanations instead of silently reaching for private APIs.
- Standard interfaces only: the project only supports iCloud features exposed through standard protocols. It will not add scraping, browser-session reuse, private endpoint reverse engineering, or other hacks to reach iCloud Drive, Photos, Notes, Reminders, or any other iCloud area without a standardized interface.
- Reduced supply-chain surface: the CLI is written in Go with no third-party Go module dependencies, which keeps builds easier to audit and limits dependency-driven supply-chain risk.
- Container portability: credentials can be supplied entirely through environment variables, logs stay outside stdout, and release binaries are published for Linux and macOS targets.
- Automation safety: destructive operations require explicit flags where appropriate, live workflows can use dry-runs and drafts, and logs intentionally avoid user content and secrets.

V1 supports:

- Mail over IMAP/SMTP, including message triage filters, decoded headers, selective text/HTML body extraction, attachment metadata and base64 retrieval, send with Sent-copy append, reply/reply-all/forward, Drafts append, flags, read state, and move/copy/delete/archive mutations.
- Calendar discovery and event CRUD over CalDAV, including calendar lookup by display name.
- Contacts address-book discovery and contact CRUD over CardDAV.
- Capability reporting for unsupported iCloud services such as Drive, Notes, Reminders, and Photos.

Missing credentials, validation errors, unsupported services, and remote failures all return structured JSON plus stable exit codes.

## Status

The current release is `v1.0.5`. The v1.0 series covers Mail, Calendar, and Contacts through documented Apple-compatible protocols and app-specific passwords. The supported surface has been validated with deterministic tests and live smoke tests against iCloud using disposable mail/calendar/contact records.

Release builds are produced for:

- Linux amd64
- Linux arm64
- macOS arm64

Download prebuilt binaries from the [GitHub releases page](https://github.com/AaronFaby/icloud-cli/releases). Each release includes platform tarballs and matching `.sha256` files.

Install with Homebrew:

```sh
brew install AaronFaby/tap/icloud
```

The Homebrew formula lives in the separate [`AaronFaby/homebrew-tap`](https://github.com/AaronFaby/homebrew-tap) repository and builds from tagged source releases.

## Agent Skill

This repository includes an agent-facing skill at [`skill/icloud-cli/SKILL.md`](skill/icloud-cli/SKILL.md). Use it when an agent needs repo-specific guidance for building, testing, configuring, or operating `icloud` in automation. The skill summarizes the documented-protocol boundary, credential handling, logging rules, common commands, live-test safety expectations, and release workflow details.

## Credentials

Environment variables are preferred for containers, CI jobs, and agents because they avoid writing credentials to disk:

```sh
export ICLOUD_APPLE_ID="name@example.com"
export ICLOUD_APP_PASSWORD="app-specific-password"
```

Create the app-specific password from your Apple Account. Apple documents the flow in [Sign in to apps with your Apple Account using app-specific passwords](https://support.apple.com/en-us/102654): sign in at `account.apple.com`, open Sign-In and Security, then select App-Specific Passwords. Two-factor authentication must be enabled on the Apple Account.

Use the generated app-specific password as `ICLOUD_APP_PASSWORD`, not your primary Apple Account password. If you reset your primary Apple Account password or revoke the app-specific password, generate a new app-specific password and update the environment variable or saved config.

Optional plaintext config file storage:

```sh
icloud auth save --apple-id name@example.com --app-password app-specific-password
```

Prefer environment variables for automation. `auth save` writes plaintext JSON and should only be used on machines where that tradeoff is acceptable.

Default config path:

```text
$HOME/.config/icloud-cli/config.json
```

Override with:

```sh
export ICLOUD_CONFIG=/path/to/config.json
```

## Logging

The CLI writes structured JSON logs outside stdout so command output remains machine-readable. Logging is configured with environment variables:

- `ICLOUD_CLI_LOG`: `file`, `stderr`, or `off`; default `file`.
- `ICLOUD_CLI_LOG_LEVEL`: `info`, `warn`, or `error`; default `warn`.
- `ICLOUD_CLI_LOG_FILE`: log file path; default is the OS user cache directory plus `icloud-cli/icloud.log`.
- `ICLOUD_CLI_LOG_SIZE`: active log file size in MB before rotation; default `10`.
- `ICLOUD_CLI_LOG_NUM`: number of historical log files to preserve; default `3`.

Inspect the effective logging configuration:

```sh
icloud log status
```

Logs include operational metadata such as command lifecycle, timings, remote status codes, resource counts, message IDs, and mutation results. Logs do not include app passwords, auth headers, SMTP auth payloads, Apple ID values, message bodies, raw RFC822 messages, vCards, iCalendar payloads, mail subjects, event summaries, or contact names.

## Exit Codes

- `0`: success
- `1`: unexpected error
- `2`: validation error
- `3`: authentication or missing credentials
- `4`: remote service or protocol error
- `5`: unsupported service

## Examples

The examples below show the common automation surface. They are intentionally shell- and JSON-friendly so the same commands can be used from local scripts, containers, or agent tools.

```sh
icloud services list
icloud auth check
icloud auth doctor
icloud mail folders list
icloud mail messages list --folder INBOX --limit 10
icloud mail messages list --folder INBOX --unread --since 24h --from domain.com --flagged --limit 10
icloud mail messages search --folder INBOX --query 'FROM "alerts@example.com"'
icloud mail messages get --folder INBOX --id 123 --raw
icloud mail messages get --folder INBOX --id 123 --body text --attachments
icloud mail messages attachment get --folder INBOX --id 123 --attachment 1
icloud mail messages reply --folder INBOX --id 123 --input-json '{"text":"Thanks"}'
icloud mail messages reply-all --folder INBOX --id 123 --input-json '{"text":"Thanks"}'
icloud mail messages forward --folder INBOX --id 123 --input-json '{"to":["person@example.com"],"text":"FYI"}'
```

All commands emit the JSON envelope by default. `--json` is accepted on every command as a no-op for automation that passes it consistently.

Mail message summaries decode encoded headers such as RFC 2047 subjects by default. Use `--raw-headers` with `mail messages list` to include `raw_subject`, `raw_from`, `raw_to`, and `raw_date` alongside decoded fields.

`mail messages get` is header-only by default. Use `--body text` for readable text, `--body html` for decoded sanitized HTML, `--attachments` for attachment metadata, and `--raw` for the full RFC822 message. Text extraction prefers useful `text/plain` parts and falls back to HTML-derived text when the plain part is missing or only a tiny stub. Retrieve attachment bytes with `mail messages attachment get --attachment <id>`; the payload is returned as `content_base64` in the JSON envelope.

Send mail:

```sh
icloud mail messages send --input-json '{
  "to": ["person@example.com"],
  "subject": "Hello",
  "text": "Sent from icloud-cli"
}'
```

Successful sends are accepted by SMTP and then copied to the detected Sent mailbox over IMAP. If SMTP succeeds but saving the sent copy fails, the command reports `sent_copy.ok=false` instead of retrying the send.

Reply, reply-all, and forward compose text-threaded messages from a source message. Replies preserve `In-Reply-To` and `References`, forwards use `Fwd:` subject handling, and actual sends use the same Sent-copy behavior as `messages send`. Pass `--dry-run` to preview recipients, subject, headers, and intended flags without sending or saving; pass `--draft` to append the composed message to Drafts instead of sending. This surface does not preserve attachments or render HTML quotes.

Move a message:

```sh
icloud mail messages move --folder INBOX --id 123 --to-folder Archive
```

Flag and read-state operations:

```sh
icloud mail messages flag --folder INBOX --id 123
icloud mail messages unflag --folder INBOX --id 123
icloud mail messages mark-read --folder INBOX --id 123
icloud mail messages mark-unread --folder INBOX --id 123
```

Delete moves to Trash by default:

```sh
icloud mail messages delete --folder INBOX --id 123
```

The CLI detects the server's `\Trash` mailbox when available, which handles iCloud folders such as `Deleted Messages`.

Permanent delete is explicit:

```sh
icloud mail messages delete --folder INBOX --id 123 --permanent --dry-run
icloud mail messages delete --folder INBOX --id 123 --permanent
```

Batch mutations accept JSON from an argument, `@file`, or stdin:

```sh
icloud mail batch move --input-json '{
  "folder": "INBOX",
  "ids": ["123", "124"],
  "to_folder": "Archive"
}'
```

Calendar and contacts discovery:

```sh
icloud calendar calendars list
icloud contacts books list
```

Calendar event CRUD:

```sh
icloud calendar events list --calendar /123/calendars/work/ --from 2026-06-08T00:00:00Z --to 2026-06-15T00:00:00Z
icloud calendar events list --calendar-name Aristotle --from 2026-06-08T00:00:00Z --to 2026-06-15T00:00:00Z
icloud calendar events create --calendar /123/calendars/work/ --input-json '{
  "summary": "Planning",
  "start": "2026-06-10T17:00:00Z",
  "end": "2026-06-10T17:30:00Z"
}'
icloud calendar events update --calendar /123/calendars/work/ --id event-20260610T170000Z --input-json '{"calendar_data":"BEGIN:VCALENDAR\nVERSION:2.0\nBEGIN:VEVENT\nUID:event-20260610T170000Z\nDTSTART:20260610T170000Z\nDTEND:20260610T173000Z\nSUMMARY:Planning\nEND:VEVENT\nEND:VCALENDAR"}'
icloud calendar events delete --calendar /123/calendars/work/ --id event-20260610T170000Z
```

Event IDs may be bare resource IDs such as `event-20260610T170000Z`, full `.ics` names, absolute hrefs returned by `events list`, or full resource URLs.

Contacts CRUD:

```sh
icloud contacts contacts list --book /123/carddavhome/card/
icloud contacts contacts get --book /123/carddavhome/card/ --id person.vcf
icloud contacts contacts create --book /123/carddavhome/card/ --input-json '{
  "formatted_name": "Ada Lovelace",
  "given_name": "Ada",
  "family_name": "Lovelace",
  "emails": ["ada@example.com"]
}'
icloud contacts contacts delete --book /123/carddavhome/card/ --id contact-20260608T120000Z
```

Use the address book entry from `contacts books list` whose `resource_types` includes `addressbook`; iCloud may also return collection roots that are not writable address books. Contact IDs may be bare IDs, `.vcf` names, hrefs returned by `contacts list`, or full resource URLs.

## Testing

Run the full test suite with workspace-local Go caches:

```sh
GOCACHE="$PWD/.gocache" GOMODCACHE="$PWD/.gomodcache" go test ./...
```

Build a local binary:

```sh
GOCACHE="$PWD/.gocache" GOMODCACHE="$PWD/.gomodcache" go build -o /private/tmp/icloud-cli ./cmd/icloud
```

For live smoke tests, use disposable records and clean them up:

- Mail: send to yourself, verify Sent copy, flag/read-state changes, then delete test mail.
- Calendar: create a temporary event, list it, update it, delete it, verify it is gone.
- Contacts: create a temporary contact in the writable address book, get it, update it, delete it, verify it returns 404.

## Unsupported Services

Apple app-specific passwords are usable with documented third-party access paths for Mail, Calendar, and Contacts. This CLI intentionally does not use private iCloud web APIs in V1. Commands for services such as Notes, iCloud Drive, Reminders, and Photos return exit code `5` with a structured JSON explanation.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE).
