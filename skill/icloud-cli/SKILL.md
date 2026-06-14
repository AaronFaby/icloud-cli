---
name: icloud-cli
description: Use when an agent needs to build, test, configure, or operate this repository's Go iCloud CLI for Apple iCloud Mail, Calendar, Contacts, and capability reporting. Trigger for tasks involving iCloud app-password credentials, JSON CLI automation, IMAP/SMTP mail operations, CalDAV calendar events, CardDAV contacts, or extending the documented-protocol iCloud service surface.
---

# iCloud CLI

Use this skill when working with this repository's `icloud` Go CLI.

## Project Purpose

This CLI is designed for agentic and scripted iCloud automation, not as a general desktop mail/calendar/contact client. Preserve these product constraints when changing behavior or docs:

- Keep commands noninteractive, JSON-first, and predictable for tool-calling loops.
- Prefer environment-driven configuration so the binary works cleanly in containers, CI jobs, and ephemeral agent sandboxes.
- Use documented protocols only unless the user explicitly approves private iCloud API work.
- Keep the supply-chain surface small. The current Go module has no third-party module dependencies; adding one should have a clear payoff and be called out in review.
- Keep stdout reserved for command JSON. Diagnostics and logs must go to stderr or the configured log file.

## Core Rules

- Use documented protocols by default: Mail via IMAP/SMTP, Calendar via CalDAV, Contacts via CardDAV.
- Do not add private iCloud web APIs, scraping, or session-cookie flows unless the user explicitly asks for that track.
- Treat credentials as secrets. Never print `ICLOUD_APP_PASSWORD`; rely on the CLI's redacted JSON output.
- Treat logs as sensitive diagnostics. Logs must contain operational metadata only, never credentials or user content.
- Prefer machine-readable CLI calls and preserve exit codes when reporting failures.

## Credentials

Credential precedence is environment first, then config file:

```sh
export ICLOUD_APPLE_ID="name@example.com"
export ICLOUD_APP_PASSWORD="app-specific-password"
export ICLOUD_CONFIG="/optional/path/config.json"
```

Config-file storage is opt-in and plaintext:

```sh
icloud auth save --apple-id name@example.com --app-password app-specific-password
```

Use `icloud auth check` to require credentials and `icloud auth doctor` for redacted diagnostics.

## Logging

Logging writes structured JSON outside stdout so command output remains machine-readable. Defaults:

```sh
ICLOUD_CLI_LOG=file
ICLOUD_CLI_LOG_LEVEL=warn
ICLOUD_CLI_LOG_SIZE=10
ICLOUD_CLI_LOG_NUM=3
```

`ICLOUD_CLI_LOG` accepts `file`, `stderr`, or `off`. `ICLOUD_CLI_LOG_FILE` overrides the default OS cache path under `icloud-cli/icloud.log`.

Use this command to inspect the effective logging configuration:

```sh
icloud log status
```

Logs may include command lifecycle, timings, remote status codes, resource counts, message IDs, and mutation results. Do not log app passwords, auth headers, SMTP auth payloads, Apple ID values, message bodies, raw RFC822 messages, vCards, iCalendar payloads, mail subjects, event summaries, or contact names.

## Build And Test

In this workspace, keep Go's build cache inside the repo:

```sh
GOCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gocache GOMODCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gomodcache go test ./...
GOCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gocache GOMODCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gomodcache go build -o /private/tmp/icloud-cli ./cmd/icloud
```

For local smoke checks that do not contact iCloud:

```sh
ICLOUD_CLI_LOG_FILE=/private/tmp/icloud-cli-smoke.log /private/tmp/icloud-cli services list
ICLOUD_CLI_LOG_FILE=/private/tmp/icloud-cli-smoke.log /private/tmp/icloud-cli log status
ICLOUD_CONFIG=/private/tmp/icloud-cli-missing.json /private/tmp/icloud-cli auth check
/private/tmp/icloud-cli notes list
```

Expected exit codes:

- `0`: success
- `2`: validation error
- `3`: missing or invalid credentials
- `4`: remote service or protocol error
- `5`: unsupported service

## Common Operations

Always pass JSON input via `--input-json`, `@file`, or stdin for create/update/send actions.

Mail:

```sh
icloud mail folders list
icloud mail messages list --folder INBOX --limit 10
icloud mail messages list --folder INBOX --unread --since 24h --from domain.com --flagged --limit 10
icloud mail messages search --folder INBOX --query 'FROM "alerts@example.com"'
icloud mail messages get --folder INBOX --id 123 --raw
icloud mail messages reply --folder INBOX --id 123 --input-json '{"text":"Thanks"}'
icloud mail messages reply-all --folder INBOX --id 123 --input-json '{"text":"Thanks"}'
icloud mail messages forward --folder INBOX --id 123 --input-json '{"to":["person@example.com"],"text":"FYI"}'
icloud mail messages move --folder INBOX --id 123 --to-folder Archive
icloud mail messages delete --folder INBOX --id 123
icloud mail batch flag --input-json '{"folder":"INBOX","ids":["123","124"]}'
```

Mail behavior notes:

- Send uses SMTP and saves a copy to the detected Sent mailbox over IMAP.
- Reply, reply-all, and forward are text-threading commands; replies preserve `In-Reply-To` and `References`, forwards use `Fwd:` subject handling, actual sends preserve Sent-copy behavior, `--dry-run` previews metadata without mutation, and `--draft` appends to Drafts without sending.
- Reply, reply-all, and forward do not provide full MIME composition; do not imply HTML-aware quoting or attachment forwarding unless that feature is added later.
- Delete moves to the detected `\Trash` mailbox by default; permanent delete requires `--permanent`.
- Message summary headers are decoded by default; `messages list --raw-headers` preserves raw subject/from/to/date fields.
- `messages get` includes parsed IMAP flags when the server returns them.
- `--json` is accepted on every command as a no-op because JSON output is already the default.

Calendar:

```sh
icloud calendar calendars list
icloud calendar events list --calendar /calendar/href/ --from 2026-06-08T00:00:00Z --to 2026-06-15T00:00:00Z
icloud calendar events list --calendar-name Aristotle --from 2026-06-08T00:00:00Z --to 2026-06-15T00:00:00Z
icloud calendar events create --calendar /calendar/href/ --input-json '{"summary":"Planning","start":"2026-06-10T17:00:00Z","end":"2026-06-10T17:30:00Z"}'
icloud calendar events update --calendar /calendar/href/ --id /calendar/href/event.ics --input-json '{"summary":"Planning updated","start":"2026-06-10T17:00:00Z","end":"2026-06-10T17:30:00Z"}'
icloud calendar events delete --calendar /calendar/href/ --id /calendar/href/event.ics
```

Contacts:

```sh
icloud contacts books list
icloud contacts contacts list --book /addressbook/href/
icloud contacts contacts create --book /addressbook/href/ --input-json '{"formatted_name":"Ada Lovelace","emails":["ada@example.com"]}'
icloud contacts contacts get --book /addressbook/href/ --id contact.vcf
icloud contacts contacts update --book /addressbook/href/ --id /addressbook/href/contact.vcf --input-json '{"formatted_name":"Ada Lovelace","emails":["ada@example.com"]}'
icloud contacts contacts delete --book /addressbook/href/ --id /addressbook/href/contact.vcf
```

For Contacts, choose the `contacts books list` entry whose `resource_types` includes `addressbook`. Do not use collection roots as writable books.

## Safety Defaults

- `mail messages delete` moves to Trash by default.
- Permanent mail deletion requires `--permanent`; use `--dry-run` before destructive automation.
- For live smoke tests, create disposable records, verify create/read/update/delete, then verify cleanup.
- Unsupported services such as iCloud Drive, Notes, Reminders, and Photos should return structured unsupported-service JSON until a private-API strategy is explicitly chosen.
