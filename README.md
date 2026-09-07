# iCloud CLI

`icloud` is a Go CLI for agentic access to Apple iCloud services through documented, app-password-compatible protocols. It is built for automation environments where predictable JSON, noninteractive auth, and a small portable binary matter more than a graphical client experience.

## Purpose

This project exists to give agents, scripts, and containerized jobs a narrow, inspectable way to work with iCloud data without browser automation or private iCloud web sessions. The design goals are:

- Agentic use: every command is noninteractive, JSON-first, and suitable for tool-calling loops that need stable output envelopes and exit codes.
- Documented protocols: Mail uses IMAP/SMTP, Calendar uses CalDAV, and Contacts uses CardDAV. Unsupported services return structured explanations instead of silently reaching for private APIs.
- Standard interfaces only: the project only supports iCloud features exposed through standard protocols. It will not add scraping, browser-session reuse, private endpoint reverse engineering, or other hacks to reach iCloud Drive, Photos, Notes, Reminders, or any other iCloud area without a standardized interface.
- Small dependency surface: beyond the Go standard library, the CLI uses Go's `golang.org/x/net` HTML parser and charset reader, with `golang.org/x/text` for character decoding. Versions are pinned in `go.mod` and verified through `go.sum`.
- Container portability: credentials can be supplied entirely through environment variables, logs stay outside stdout, and release binaries are published for Linux and macOS targets.
- Automation safety: destructive operations require explicit flags where appropriate, live workflows can use dry-runs and drafts, and logs intentionally avoid user content and secrets.

V1 supports:

- Mail over IMAP/SMTP, including cursor-based polling, triage filters, decoded headers, selective body extraction, attachment retrieval and sending, reply/reply-all/forward, Drafts append, Sent-copy append, and message mutations.
- Calendar discovery and event CRUD over CalDAV, including partial edits, all-day events, explicit timezones, and calendar lookup by display name.
- Contacts address-book discovery, search, CRUD, and partial edits over CardDAV.
- Capability reporting for unsupported iCloud services such as Drive, Notes, Reminders, and Photos.

Missing credentials, validation errors, unsupported services, and remote failures all return structured JSON plus stable exit codes.

## Status

The current release is `v1.1.0`. It covers Mail, Calendar, and Contacts through documented Apple-compatible protocols and app-specific passwords. Earlier functionality has been smoke-tested against iCloud using disposable records.

Version 1.1.0 adds attachments, polling, partial edits, timezone/all-day events, contact search, and security hardening. These changes have automated test coverage and code/security review but have not yet had live iCloud validation. Download the tagged release assets for this version; Homebrew availability follows the separate tap update.

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

Optional plaintext config file storage, using credentials already supplied through the environment:

```sh
icloud auth save
```

Prefer environment variables injected by your secret manager or CI for automation. Avoid typing literal secrets into shell commands that may be saved in history. `auth save` reads `ICLOUD_APPLE_ID` and `ICLOUD_APP_PASSWORD` when the corresponding flags are omitted; explicit flags remain supported but may expose values through process arguments. It writes plaintext JSON and should only be used on machines where that tradeoff is acceptable.

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
- `1`: unexpected error or failure writing the JSON output
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

Nested command and group `--help` returns JSON and exits successfully without credentials or network access. Command arguments must use named flags; unexpected positional arguments are rejected so trailing safety flags cannot be silently ignored.

Mail message summaries decode encoded headers such as RFC 2047 subjects by default. Use `--raw-headers` with `mail messages list` to include `raw_subject`, `raw_from`, `raw_to`, and `raw_date` alongside decoded fields.

`--since` uses IMAP's calendar-day precision: `--since 24h` includes messages from the resulting date, rather than enforcing an exact rolling 24-hour cutoff. Plain-text searches and the list command's `--from` filter support Unicode; raw IMAP search criteria must be ASCII.

`mail messages get` is header-only by default. Use `--body text` for readable text, `--body html` for decoded sanitized HTML, `--attachments` for attachment metadata, and `--raw` for the full RFC822 message. Text extraction prefers useful `text/plain` parts and falls back to HTML-derived text when the plain part is missing or only a tiny stub. Retrieve attachment bytes with `mail messages attachment get --attachment <id>`; the payload is returned as `content_base64` in the JSON envelope.

Mail parsing is bounded: messages and IMAP literals are limited to 32 MiB, MIME nesting to 16 levels, MIME entities to 1,000, and cumulative decoded reads to 128 MiB. Multipart content is streamed; metadata-only attachment reads discard file bytes. IMAP responses are limited to 64 MiB, 100,000 lines, and 1 MiB per line. Exceeding a limit returns an error.

Decoded and sanitized HTML are limited to 4 MiB, 50,000 tokens and attributes combined, and 1 MiB per token. Input is checked before tree construction; text extraction reuses the sanitized tree. Overly large or complex HTML returns an explicit error.

Send mail:

```sh
icloud mail messages send --input-json '{
  "to": ["person@example.com"],
  "subject": "Hello",
  "text": "Sent from icloud-cli"
}'
```

Attach local regular files or base64 data with an `attachments` array:

```sh
icloud mail messages send --input-json '{
  "to": ["person@example.com"], "subject": "Report", "text": "Attached.",
  "attachments": [{"path":"/tmp/report.pdf","content_type":"application/pdf"}]
}'
icloud mail messages forward --folder INBOX --id 123 --dry-run --input-json '{
  "to":["person@example.com"], "text":"FYI", "include_attachments":true
}'
```

Each attachment accepts `path` or `content_base64`, with optional `filename` and `content_type` (default `application/octet-stream`). Reply, reply-all, and forward also accept new attachments. Only forward accepts `include_attachments` to copy source attachments; it defaults to false. Dry-run previews include filename, content type, and size, without file paths or base64 content. Limits are 100 attachments, 20 MiB total decoded attachment bytes, and 32 MiB for the complete encoded message.

Successful sends are accepted by SMTP and then copied to the detected Sent mailbox over IMAP. If SMTP succeeds but saving the sent copy fails, the command reports `sent_copy.ok=false` instead of retrying the send.

Outgoing headers exceeding the 998-byte encoded line limit return a validation error before sending or saving a draft. Shorten the field or recipient list when this occurs.

IMAP sessions have a 30-second deadline; SMTP sends and DAV operations have 45-second deadlines. Saving a Sent copy uses a separate IMAP session. Once SMTP accepts the message, a cleanup failure does not turn the send into a failure that could trigger a duplicate retry.

Reply, reply-all, and forward compose text-threaded messages from a source message. Replies preserve `In-Reply-To` and `References`, forwards use `Fwd:` subject handling, and actual sends use the same Sent-copy behavior as `messages send`. Pass `--dry-run` to preview recipients, subject, headers, attachments, and intended flags without sending or saving; pass `--draft` to append the composed message to Drafts instead of sending. Quotes remain plain text.

Poll for newly added messages using a reusable cursor:

```sh
icloud mail messages poll --folder INBOX --start-now
icloud mail messages poll --folder INBOX --cursor "$CURSOR" --limit 100
```

Read `data.messages`, `data.next_cursor`, and `data.has_more` from the JSON envelope. Save the next cursor only after successfully processing the page; retrying an earlier cursor can repeat messages. Without a cursor, polling includes existing mail; `--start-now` instead returns a checkpoint after current mail and cannot be combined with a cursor. Follow `has_more` to finish the current snapshot, then reuse the completed cursor to check for later arrivals. Polling returns ascending UIDs and does not mark messages read. Cursors belong to one account and folder; a `mailbox_reset` error requires explicitly restarting. This detects new messages, not changes to flags or deletions. Limits are 1–1,000 messages per page (default 100) and 32 MiB of fetched headers per page.

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
  "id": "planning-example",
  "summary": "Planning",
  "start": "2026-06-10T17:00:00Z",
  "end": "2026-06-10T17:30:00Z"
}'
icloud calendar events update --calendar /123/calendars/work/ --id planning-example --input-json '{"summary":"Planning updated","start":"2026-06-10T17:00:00Z","end":"2026-06-10T17:30:00Z"}'
icloud calendar events delete --calendar /123/calendars/work/ --id planning-example
```

Event IDs may be bare resource IDs such as `planning-example`, full `.ics` names, absolute hrefs returned by `events list`, or full resource URLs. The example supplies an ID to make the sequence reproducible; when omitting it, use the returned resource href for subsequent operations.

Creates generate random IDs when omitted and reject an existing resource instead of overwriting it. Updates require a target ID and an existing resource. Structured updates preserve the stored UID and use its ETag to reject concurrent changes when the server provides one. Calendar and contact updates replace the complete resource; include all fields you want to retain. Use `calendar_data` or `vcard` with the existing UID to preserve fields outside the structured input, such as recurrence, alarms, attendees, or additional contact properties.

Calendar listing accepts either or both time bounds; omit a bound for an open-ended range. Times must be valid RFC3339 values or compact UTC values such as `20260610T170000Z`. Credentialed DAV requests require HTTPS, including redirects.

Create an all-day event with date-only values and an exclusive end date, or provide local wall times with an IANA timezone:

```sh
icloud calendar events create --calendar /123/calendars/work/ --input-json '{"summary":"Away","all_day":true,"start":"2026-09-10","end":"2026-09-12"}'
icloud calendar events create --calendar /123/calendars/work/ --input-json '{"summary":"Planning","start":"2026-09-10T09:00","end":"2026-09-10T09:30","time_zone":"America/Los_Angeles"}'
```

All-day events cannot specify `time_zone`. Timed events are stored as UTC instants; timezone data is embedded for containers. Nonexistent or ambiguous local times during daylight-saving transitions are rejected; use RFC3339 times with an explicit valid offset to disambiguate. When also supplying `time_zone`, the offset must agree with that zone. Timed inputs require whole-second precision and years 1–9999 after UTC conversion.

Use `patch` to change selected fields while retaining other stored properties:

```sh
icloud calendar events patch --calendar /123/calendars/work/ --id planning-example --input-json '{"summary":"Revised planning","location":null}'
icloud contacts contacts patch --book /123/carddavhome/card/ --id ada-example --input-json '{"organization":"Example","note":null}'
```

Omitted fields remain unchanged; `null` clears optional fields, while required names and event times cannot be cleared. Unknown patch keys are errors. Contact patches support `formatted_name`, `given_name`, `family_name`, `emails`, `phones`, `organization`, and `note`; arrays replace the corresponding email or phone properties. Event patches support `summary`, `description`, `location`, `start`, `end`, `all_day`, and `time_zone`. Patches preserve the stored UID and untargeted properties, including alarms and recurrence components, and require a verified strong ETag. A concurrent change fails instead of overwriting it. Existing `update` commands still replace the full resource.

A single event endpoint can be patched when the other stored endpoint can be resolved. Changed timed endpoints serialize UTC; omitted endpoints remain verbatim. Changing `all_day` or `time_zone` requires both `start` and `end`. Metadata patches support recurring events; temporal patches of recurring events are rejected to avoid changing recurrence semantics. Recurrence-aware time editing and new recurrence creation are outside this structured feature set. DAV GET and XML response bodies are limited to 32 MiB; content parsing allows 100,000 physical lines and 32 component levels.

Event/contact deletion first verifies that the target is an individual resource of the expected service, then deletes it conditionally using its strong ETag. Collections, missing or ambiguous metadata, and weak or missing ETags are rejected. Delete requests do not follow redirects; legitimate absolute resource hrefs and opaque resource names remain supported.

Contacts CRUD:

```sh
icloud contacts contacts list --book /123/carddavhome/card/
icloud contacts contacts create --book /123/carddavhome/card/ --input-json '{
  "id": "ada-example",
  "formatted_name": "Ada Lovelace",
  "given_name": "Ada",
  "family_name": "Lovelace",
  "emails": ["ada@example.com"]
}'
icloud contacts contacts get --book /123/carddavhome/card/ --id ada-example
icloud contacts contacts update --book /123/carddavhome/card/ --id ada-example --input-json '{"formatted_name":"Ada Lovelace","given_name":"Ada","family_name":"Lovelace","emails":["ada@example.com"],"organization":"Example"}'
icloud contacts contacts delete --book /123/carddavhome/card/ --id ada-example
```

Use the address book entry from `contacts books list` whose `resource_types` includes `addressbook`; iCloud may also return collection roots that are not writable address books. Contact IDs may be bare IDs, `.vcf` names, hrefs returned by `contacts list`, or full resource URLs.

Search contacts on the server:

```sh
icloud contacts contacts search --book /123/carddavhome/card/ --query Ada --limit 20
icloud contacts contacts search --book /123/carddavhome/card/ --email example.com
```

Specify exactly one of `--query`, `--name`, `--email`, `--phone`, or `--organization`. Matching is case-insensitive substring matching; `--query` searches across all these fields. Results include href, ETag, raw vCard, and normalized `contact` fields. The limit defaults to 100 and accepts 1–1,000 results.

## Testing

Builds require Go 1.25 or newer. CI uses the current stable Go release and runs vet and race tests before building release binaries.

Run the full test suite with workspace-local Go caches:

```sh
GOCACHE="$PWD/.gocache" GOMODCACHE="$PWD/.gomodcache" go test ./...
GOCACHE="$PWD/.gocache" GOMODCACHE="$PWD/.gomodcache" go vet ./...
GOCACHE="$PWD/.gocache" GOMODCACHE="$PWD/.gomodcache" go test -race ./...
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
