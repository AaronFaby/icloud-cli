# iCloud CLI

`icloud` is a Go CLI for agentic access to Apple iCloud services through documented, app-password-compatible protocols.

V1 supports:

- Mail over IMAP/SMTP.
- Calendar discovery and event CRUD over CalDAV.
- Contacts address-book discovery and contact CRUD over CardDAV.
- Capability reporting for unsupported iCloud services such as Drive, Notes, Reminders, and Photos.

The CLI is JSON-first and noninteractive. Missing credentials, validation errors, unsupported services, and remote failures all return structured JSON plus stable exit codes.

## Status

`v1.0.0` covers Mail, Calendar, and Contacts through documented Apple-compatible protocols and app-specific passwords. The v1.0 surface has been validated with deterministic tests and live smoke tests against iCloud using disposable mail/calendar/contact records.

Release builds are produced for:

- Linux amd64
- Linux arm64
- macOS arm64

## Credentials

Environment variables are preferred for containers and agents:

```sh
export ICLOUD_APPLE_ID="name@example.com"
export ICLOUD_APP_PASSWORD="app-specific-password"
```

Optional plaintext config file storage:

```sh
icloud auth save --apple-id name@example.com --app-password app-specific-password
```

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

```sh
icloud services list
icloud auth check
icloud auth doctor
icloud mail folders list
icloud mail messages list --folder INBOX --limit 10
icloud mail messages search --folder INBOX --query 'FROM "alerts@example.com"'
icloud mail messages get --folder INBOX --id 123 --raw
```

Send mail:

```sh
icloud mail messages send --input-json '{
  "to": ["person@example.com"],
  "subject": "Hello",
  "text": "Sent from icloud-cli"
}'
```

Successful sends are accepted by SMTP and then copied to the detected Sent mailbox over IMAP. If SMTP succeeds but saving the sent copy fails, the command reports `sent_copy.ok=false` instead of retrying the send.

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
