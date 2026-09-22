# Agent Guidance

## Scope and references

`icloud` is a noninteractive, JSON-first Go CLI for iCloud automation. Keep it portable, predictable for agents and scripts, and small enough to audit.

- Use documented protocols: IMAP/SMTP for Mail, CalDAV for Calendar, and CardDAV for Contacts. Private iCloud APIs, web-session scraping, and Drive/Notes/Reminders/Photos support require an explicit user decision.
- Reuse the standard library and existing helpers. The module also uses `golang.org/x/net` for HTML parsing/charset support and `golang.org/x/text` for decoding; justify further dependencies.
- Use [README.md](README.md) for command examples, exact resource limits, and compatibility notes. Keep it synchronized with behavior, along with [skill/icloud-cli/SKILL.md](skill/icloud-cli/SKILL.md).

## CLI, credentials, and logging

- Preserve JSON output and stable exit codes. `--json` is a no-op; nested `--help` must return a successful JSON envelope without credentials or network access. Reject unexpected positional arguments before operations. Failure to write JSON must return a nonzero exit code.
- Credential values come from `ICLOUD_APPLE_ID` and `ICLOUD_APP_PASSWORD` first, then the config file. `ICLOUD_CONFIG` selects the default config path; an explicit `--config` overrides it.
- Write plaintext credentials only when the user explicitly runs `icloud auth save`. Prefer environment-supplied credentials, keep them out of flag defaults/help, and preserve atomic mode-0600 config replacement.
- Logs contain operational metadata only. Redact command arguments and error text; never log credentials, auth headers/payloads, Apple IDs, message bodies or subjects, raw RFC822, vCards, iCalendar payloads, event summaries, or contact names. Attachment previews must omit paths and content.
- Logging is environment-configured: `ICLOUD_CLI_LOG=file|stderr|off` (default `file`), `ICLOUD_CLI_LOG_LEVEL=info|warn|error` (default `warn`), `ICLOUD_CLI_LOG_FILE` (default OS cache path `icloud-cli/icloud.log`), `ICLOUD_CLI_LOG_SIZE` (default 10 MB), and `ICLOUD_CLI_LOG_NUM` (default 3). Use `icloud log status` to inspect effective settings.

## Mail contracts

- Message summaries decode headers by default; `--raw-headers` preserves raw fields. Message retrieval is header-only unless `--body text|html`, `--attachments`, or `--raw` is requested. Text extraction prefers useful plain text and falls back to HTML-derived text when plain text is absent or a tiny stub. Attachment retrieval returns `content_base64`.
- Preserve triage filters (`--unread`, `--since`, `--from`, `--flagged`, `--limit`). `--since` has IMAP calendar-day precision. Plain-text search and `--from` support Unicode; raw IMAP criteria must be ASCII and must not include an IMAP literal (`{n}` or `{n+}`).
- Replies preserve threading headers; forwards use `Fwd:` subjects. Preserve metadata-only `--dry-run`, Drafts append via `--draft`, and Sent-copy append after sending. Never retry an SMTP-accepted message solely because cleanup or Sent-copy append failed; expose `sent_copy.ok` separately.
- Send/reply/reply-all/forward accept attachments from regular-file `path` or `content_base64`, with optional `filename`/`content_type`. Copy source attachments only for forward with `include_attachments:true`. Preserve count, decoded-byte, and encoded-message limits and nonblocking rejection of nonregular files.
- Poll returns `messages`, `next_cursor`, and `has_more`. An initial poll includes existing mail unless `--start-now` is supplied. Bind cursors to account, folder, and UIDVALIDITY; detect mailbox resets, preserve snapshot paging, and never advance checkpoints on errors. Consumers save cursors after processing successful pages. Polling must not mark mail read.
- Preserve bounded IMAP/SMTP I/O, UID/mailbox/flag validation, and valid address/header serialization. MIME decoders share one read budget; HTML limits apply before tree construction. Limit failures must be explicit errors.

## Calendar and contacts contracts

- Credentialed DAV requests require HTTPS and allowlisted hosts, including redirects. Preserve legitimate opaque absolute resource hrefs while rejecting unsafe targets.
- Creates must not overwrite existing resources. Full-replacement `update` requires an existing target; structured updates preserve its UID and use its ETag when available.
- `patch` preserves omitted properties, UID, alarms, recurrence components, and custom fields. Optional JSON `null` clears a field; unknown keys and clearing required fields fail. Contact email/phone arrays replace their corresponding properties. Require verified individual-resource metadata, a strong ETag, conditional writes, and redirect-free patch GET/PUT requests.
- Deletes require positive individual-resource metadata of the expected service and a strong ETag. Use conditional DELETE and refuse redirects, collections, or unverifiable targets.
- All-day events use date-only start and exclusive end dates, without a timezone. Timed input supports IANA `time_zone`; embed timezone data and reject ambiguous/nonexistent local times unless a valid explicit offset resolves them. Changed timed endpoints serialize UTC; omitted endpoints remain verbatim. Mode/zone patches require both endpoints. Reject temporal patches of recurring events; metadata patches remain supported.
- Event listing accepts `--calendar` hrefs or `--calendar-name` lookup. Contact search uses CardDAV filtering, accepts exactly one query/name/email/phone/organization criterion, and returns normalized fields.
- Preserve DAV response, physical-line, and component-depth limits. Handle folded properties without quadratic copying.

## Build and validation

Use Go 1.26 or newer, preferably a current patched release. Run from the repository root with workspace-local caches:

```sh
export GOCACHE="$PWD/.gocache" GOMODCACHE="$PWD/.gomodcache"
go test ./...
go vet ./...
go test -race ./...
go build -o /tmp/icloud-cli ./cmd/icloud
```

For code changes, run tests, vet, and race checks, with focused regressions for changed behavior. Keep automated/mock results distinct from live iCloud validation.

When running live tests, use disposable records and clean them up in the same run. For Contacts, select the `contacts books list` entry whose `resource_types` includes `addressbook`; collection roots are not writable books.

## Releases and Homebrew

- Tags matching `v*` trigger tests and binary builds for Linux amd64, Linux arm64, and macOS arm64, then publish archives and SHA-256 files. Verify the published notes, assets, and checksums.
- Release notes come from the annotated tag. Preserve Markdown headings with `git tag --cleanup=verbatim` when using a notes file; verify the published body and use `gh release edit --notes-file` if needed.
- The formula lives in `AaronFaby/homebrew-tap` and builds from tagged source. Users install with `brew install AaronFaby/tap/icloud`; formula changes should land through tap PRs.
- The release workflow dispatches `bump-icloud.yml` using `HOMEBREW_TAP_TOKEN`. Check release publication and tap handoff separately; a dispatch failure can occur after successful publication. Verify formula validation and the resulting tap PR.
