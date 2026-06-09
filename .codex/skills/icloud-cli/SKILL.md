---
name: icloud-cli
description: Use when an agent needs to build, test, configure, or operate this repository's Go iCloud CLI for Apple iCloud Mail, Calendar, Contacts, and capability reporting. Trigger for tasks involving iCloud app-password credentials, JSON CLI automation, IMAP/SMTP mail operations, CalDAV calendar events, CardDAV contacts, or extending the documented-protocol iCloud service surface.
---

# iCloud CLI

Use this skill when working with this repo's `icloud` Go CLI.

## Core Rules

- Use documented protocols by default: Mail via IMAP/SMTP, Calendar via CalDAV, Contacts via CardDAV.
- Do not add private iCloud web APIs, scraping, or session-cookie flows unless the user explicitly asks for that track.
- Treat credentials as secrets. Never print `ICLOUD_APP_PASSWORD`; rely on the CLI's redacted JSON output.
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

## Build And Test

In this workspace, keep Go's build cache inside the repo:

```sh
GOCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gocache go test ./...
GOCACHE=/Users/aaronfaby/Projects/Codex/icloud-cli/.gocache go build -o /private/tmp/icloud-cli ./cmd/icloud
```

For local smoke checks that do not contact iCloud:

```sh
/private/tmp/icloud-cli services list
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
icloud mail messages search --folder INBOX --query 'FROM "alerts@example.com"'
icloud mail messages get --folder INBOX --id 123 --raw
icloud mail messages move --folder INBOX --id 123 --to-folder Archive
icloud mail messages delete --folder INBOX --id 123
icloud mail batch flag --input-json '{"folder":"INBOX","ids":["123","124"]}'
```

Calendar:

```sh
icloud calendar calendars list
icloud calendar events list --calendar /calendar/href/ --from 2026-06-08T00:00:00Z --to 2026-06-15T00:00:00Z
icloud calendar events create --calendar /calendar/href/ --input-json '{"summary":"Planning","start":"2026-06-10T17:00:00Z","end":"2026-06-10T17:30:00Z"}'
```

Contacts:

```sh
icloud contacts books list
icloud contacts contacts list --book /addressbook/href/
icloud contacts contacts create --book /addressbook/href/ --input-json '{"formatted_name":"Ada Lovelace","emails":["ada@example.com"]}'
```

## Safety Defaults

- `mail messages delete` moves to Trash by default.
- Permanent mail deletion requires `--permanent`; use `--dry-run` before destructive automation.
- Unsupported services such as iCloud Drive, Notes, Reminders, and Photos should return structured unsupported-service JSON until a private-API strategy is explicitly chosen.
