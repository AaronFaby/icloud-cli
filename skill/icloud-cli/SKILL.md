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
- Keep the supply-chain surface small. The module uses Go's `golang.org/x/net` for HTML parsing and charset reading, with `golang.org/x/text` for decoding. Any further dependency needs a clear payoff and should be called out in review.
- Keep stdout reserved for command JSON. Diagnostics and logs must go to stderr or the configured log file.
- Mail fetches are header-only unless content is requested with `--raw`, `--body text|html`, or `--attachments`; `--body text` prefers useful plain text and falls back to HTML-derived text when the plain part is missing or only a tiny stub. Retrieve attachment bytes with `messages attachment get` and expect base64 JSON.

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

Config-file storage is opt-in and plaintext. With credentials already injected into the environment:

```sh
icloud auth save
```

Prefer secret-manager or CI environment injection; avoid literal secrets in shell history. `auth save` uses the credential environment variables when its corresponding flags are omitted. Explicit credential flags remain supported but can expose values through process arguments. Use `icloud auth check` to require credentials and `icloud auth doctor` for redacted diagnostics.

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

Use Go 1.25 or newer, preferably a current patched release. In this workspace, keep Go's build cache inside the repo:

```sh
GOCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gocache GOMODCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gomodcache go test ./...
GOCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gocache GOMODCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gomodcache go vet ./...
GOCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gocache GOMODCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gomodcache go test -race ./...
GOCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gocache GOMODCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gomodcache go build -o /private/tmp/icloud-cli ./cmd/icloud
```

Install the released CLI with Homebrew:

```sh
brew install AaronFaby/tap/icloud
```

The Homebrew formula lives in `AaronFaby/homebrew-tap` and builds from tagged source releases. Tags matching `v*` publish binary assets from this repo; future tag releases can dispatch the tap workflow to open a formula bump pull request.

For local smoke checks that do not contact iCloud:

```sh
ICLOUD_CLI_LOG_FILE=/private/tmp/icloud-cli-smoke.log /private/tmp/icloud-cli services list
ICLOUD_CLI_LOG_FILE=/private/tmp/icloud-cli-smoke.log /private/tmp/icloud-cli log status
ICLOUD_CONFIG=/private/tmp/icloud-cli-missing.json /private/tmp/icloud-cli auth check
/private/tmp/icloud-cli notes list
```

Expected exit codes:

- `0`: success
- `1`: unexpected error or failure writing JSON output
- `2`: validation error
- `3`: missing or invalid credentials
- `4`: remote service or protocol error
- `5`: unsupported service

## Common Operations

Always pass JSON input via `--input-json`, `@file`, or stdin for create/update/patch/send actions.

Mail:

```sh
icloud mail folders list
icloud mail messages list --folder INBOX --limit 10
icloud mail messages poll --folder INBOX --start-now
icloud mail messages poll --folder INBOX --cursor "$CURSOR" --limit 100
icloud mail messages list --folder INBOX --unread --since 24h --from domain.com --flagged --limit 10
icloud mail messages search --folder INBOX --query 'FROM "alerts@example.com"'
icloud mail messages get --folder INBOX --id 123 --raw
icloud mail messages get --folder INBOX --id 123 --body text --attachments
icloud mail messages attachment get --folder INBOX --id 123 --attachment 1
icloud mail messages reply --folder INBOX --id 123 --input-json '{"text":"Thanks"}'
icloud mail messages reply-all --folder INBOX --id 123 --input-json '{"text":"Thanks"}'
icloud mail messages forward --folder INBOX --id 123 --input-json '{"to":["person@example.com"],"text":"FYI"}'
icloud mail messages move --folder INBOX --id 123 --to-folder Archive
icloud mail messages delete --folder INBOX --id 123
icloud mail batch flag --input-json '{"folder":"INBOX","ids":["123","124"]}'
```

Mail behavior notes:

- Send uses SMTP and saves a copy to the detected Sent mailbox over IMAP.
- Once SMTP accepts a message, cleanup or Sent-copy failure must not trigger a duplicate send; inspect `sent_copy.ok`. IMAP sessions are bounded to 30 seconds, SMTP sends and DAV operations to 45 seconds.
- Outgoing encoded header lines over 998 bytes are rejected before sending or saving a draft.
- Reply, reply-all, and forward are text-threading commands; replies preserve `In-Reply-To` and `References`, forwards use `Fwd:` subject handling, actual sends preserve Sent-copy behavior, `--dry-run` previews metadata without mutation, and `--draft` appends to Drafts without sending.
- Send, reply, reply-all, and forward accept an `attachments` array: each entry uses `path` (regular file) or `content_base64`, plus optional `filename` and `content_type`. Forward alone accepts `include_attachments:true` to copy source files; default false. Previews expose filename/type/size, not paths or content. Quotes remain plain text. Limits: 100 attachments, 20 MiB decoded total, 32 MiB encoded message.
- Poll returns `messages`, `next_cursor`, and `has_more`. Save the next cursor only after processing a successful page; retries may repeat messages. Default first poll includes existing mail; `--start-now` checkpoints after current mail and cannot combine with `--cursor`. Drain `has_more` pages, then reuse the completed cursor for future arrivals. Cursors bind to account/folder/UIDVALIDITY; `mailbox_reset` requires explicit restart. Poll does not mark read or report flag/deletion changes. Page limits: 1–1,000 messages (default100), 32 MiB fetched headers.
- `messages get` is header-only unless `--raw`, `--body text|html`, or `--attachments` is passed; `--body text` may use HTML-derived text when the plain part is missing or only a tiny stub. `messages attachment get --attachment <id>` returns one attachment as `content_base64`.
- Delete moves to the detected `\Trash` mailbox by default; permanent delete requires `--permanent`.
- Message summary headers are decoded by default; `messages list --raw-headers` preserves raw subject/from/to/date fields.
- `--since` has calendar-day precision. Unicode is supported in plain-text searches and `--from`; raw IMAP criteria must be ASCII.
- `messages get` includes parsed IMAP flags when the server returns them.
- `--json` is accepted on every command as a no-op because JSON output is already the default.
- Nested group/command help is offline and returns exit 0. Unexpected positional arguments are validation errors; all command arguments must use named flags.

Calendar:

```sh
icloud calendar calendars list
icloud calendar events list --calendar /calendar/href/ --from 2026-06-08T00:00:00Z --to 2026-06-15T00:00:00Z
icloud calendar events list --calendar-name Aristotle --from 2026-06-08T00:00:00Z --to 2026-06-15T00:00:00Z
icloud calendar events create --calendar /calendar/href/ --input-json '{"id":"event","summary":"Planning","start":"2026-06-10T17:00:00Z","end":"2026-06-10T17:30:00Z"}'
icloud calendar events update --calendar /calendar/href/ --id /calendar/href/event.ics --input-json '{"summary":"Planning updated","start":"2026-06-10T17:00:00Z","end":"2026-06-10T17:30:00Z"}'
icloud calendar events patch --calendar /calendar/href/ --id /calendar/href/event.ics --input-json '{"summary":"Revised planning","location":null}'
icloud calendar events delete --calendar /calendar/href/ --id /calendar/href/event.ics
```

Contacts:

```sh
icloud contacts books list
icloud contacts contacts list --book /addressbook/href/
icloud contacts contacts search --book /addressbook/href/ --email example.com --limit 20
icloud contacts contacts create --book /addressbook/href/ --input-json '{"id":"contact","formatted_name":"Ada Lovelace","emails":["ada@example.com"]}'
icloud contacts contacts get --book /addressbook/href/ --id contact.vcf
icloud contacts contacts update --book /addressbook/href/ --id /addressbook/href/contact.vcf --input-json '{"formatted_name":"Ada Lovelace","emails":["ada@example.com"]}'
icloud contacts contacts patch --book /addressbook/href/ --id /addressbook/href/contact.vcf --input-json '{"organization":"Example"}'
icloud contacts contacts delete --book /addressbook/href/ --id /addressbook/href/contact.vcf
```

For Contacts, choose the `contacts books list` entry whose `resource_types` includes `addressbook`. Do not use collection roots as writable books.

Calendar/contact creates reject an existing resource. Updates require a target ID and replace the complete resource; provide all fields to retain. Structured updates preserve the stored UID and use its ETag when available. Use raw `calendar_data` or `vcard` with the existing UID to retain properties outside the structured input, such as recurrence, alarms, or additional contact fields. DAV hrefs must use HTTPS.

Prefer `patch` for selected fields: omitted properties stay unchanged, `null` clears optional values, unknown keys fail, and required names/times cannot be cleared. Patches require verified strong ETags and reject concurrent changes. Untargeted recurrence, alarms, and custom properties remain verbatim. Contact email/phone arrays replace all corresponding properties. A single event endpoint can change if the other stored endpoint is resolvable; changing `all_day` or `time_zone` requires both endpoints. Temporal patches of recurring events are rejected; metadata patches remain supported.

Event creation accepts `all_day:true` with date-only `start`/exclusive `end`, or local wall times with `time_zone` such as `America/Los_Angeles`. Timed creates and changed patch endpoints serialize UTC; omitted endpoints remain verbatim. Embedded timezone data supports containers. DST gaps/folds require valid explicit-offset input; supplied offsets must agree with the named zone. All-day events cannot specify a timezone. Timed inputs require whole seconds and UTC years 1–9999.

Contact search requires exactly one of `--query`, `--name`, `--email`, `--phone`, or `--organization`, using server-side case-insensitive substring matching. Results include normalized `contact` fields and the original vCard. Limit defaults to100, range1–1,000. DAV GET/XML responses are capped at32MiB.

Deletes verify individual-resource metadata and require a strong ETag before issuing a conditional DELETE. Collection targets, wrong-service types, unverifiable metadata, and DELETE redirects are rejected. Mail parsing streams multipart bodies and enforces 32 MiB messages/literals, 16 MIME nesting levels, 1,000 MIME entities, and a shared 128 MiB decoded-read budget; oversized or overly complex input returns an error.

HTML is checked before tree construction: decoded and sanitized HTML must fit 4 MiB, 50,000 tokens and attributes combined, and 1 MiB per token. Limit failures propagate to body extraction and reply preparation rather than returning an empty successful body.

## Safety Defaults

- `mail messages delete` moves to Trash by default.
- Permanent mail deletion requires `--permanent`; use `--dry-run` before destructive automation.
- For live smoke tests, create disposable records, verify create/read/update/delete, then verify cleanup.
- Unsupported services such as iCloud Drive, Notes, Reminders, and Photos should return structured unsupported-service JSON until a private-API strategy is explicitly chosen.
