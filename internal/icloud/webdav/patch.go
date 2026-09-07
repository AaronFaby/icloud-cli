package webdav

import (
	"context"
	"encoding/json"
	"github.com/aaronfaby/icloud-cli/internal/output"
	"net/http"
	"slices"
	"strings"
	"time"
)

type contentLine struct {
	raw, head, name, value string
	depth                  int
}

func badPatch() error {
	return output.Validation("invalid_patch", "patch fields or stored resource are invalid or ambiguous", nil)
}

// Keep physical lines verbatim unless their property is explicitly replaced.
func parseContent(data, component string) ([]contentLine, error) {
	if len(data) > maxDAVBody {
		return nil, badPatch()
	}
	var lines []contentLine
	offset, start, physical := 0, 0, 0
	for raw := range strings.Lines(data) {
		physical++
		if physical > 100000 {
			return nil, badPatch()
		}
		continuation := raw[0] == ' ' || raw[0] == '\t'
		if continuation && offset == 0 {
			return nil, badPatch()
		}
		if !continuation && offset > start {
			lines = append(lines, contentLine{raw: data[start:offset]})
			start = offset
		}
		offset += len(raw)
	}
	if offset > start {
		lines = append(lines, contentLine{raw: data[start:offset]})
	}

	var stack []string
	roots, targets := 0, 0
	for i := range lines {
		l := &lines[i]
		s := strings.ReplaceAll(strings.ReplaceAll(l.raw, "\r\n ", ""), "\r\n\t", "")
		s = strings.ReplaceAll(strings.ReplaceAll(s, "\n ", ""), "\n\t", "")
		s = strings.TrimRight(s, "\r\n")
		quoted := false
		colon := -1
		for j, r := range s {
			if r == '"' {
				quoted = !quoted
			}
			if r == ':' && !quoted {
				colon = j
				break
			}
		}
		if colon < 1 || strings.ContainsAny(s, "\r\n\x00") {
			return nil, badPatch()
		}
		l.head = s[:colon]
		l.value = s[colon+1:]
		l.name = strings.ToUpper(strings.Split(l.head, ";")[0])
		if dot := strings.LastIndex(l.name, "."); dot >= 0 {
			l.name = l.name[dot+1:]
		}
		if len(stack) == 0 && l.name != "BEGIN" {
			return nil, badPatch()
		}
		l.depth = len(stack)
		if l.name == "BEGIN" {
			if len(stack) == 0 {
				roots++
			}
			if len(stack) >= 32 {
				return nil, badPatch()
			}
			stack = append(stack, strings.ToUpper(l.value))
			if strings.EqualFold(l.value, component) {
				targets++
			}
		}
		if l.name == "END" {
			if len(stack) == 0 || stack[len(stack)-1] != strings.ToUpper(l.value) {
				return nil, badPatch()
			}
			stack = stack[:len(stack)-1]
		}
	}
	root := component
	if component == "VEVENT" {
		root = "VCALENDAR"
	}
	if len(lines) == 0 || lines[0].name != "BEGIN" || !strings.EqualFold(lines[0].value, root) || len(stack) != 0 || roots != 1 || targets < 1 {
		return nil, badPatch()
	}
	return lines, nil
}
func escapedText(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\r", "", "\n", `\n`, ";", `\;`, ",", `\,`).Replace(s)
}
func splitText(s string, sep byte) []string {
	var out []string
	start := 0
	escaped := false
	for i := 0; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		if s[i] == '\\' {
			escaped = true
			continue
		}
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
func unescapeText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			if s[i] == 'n' || s[i] == 'N' {
				b.WriteByte('\n')
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (c *Client) PatchEvent(ctx context.Context, collection, id string, patch map[string]json.RawMessage) (Resource, error) {
	return c.patchResource(ctx, c.eventURL(collection, id), "VEVENT", "text/calendar", patch)
}
func (c *Client) PatchContact(ctx context.Context, collection, id string, patch map[string]json.RawMessage) (Resource, error) {
	return c.patchResource(ctx, c.contactURL(collection, id), "VCARD", "text/vcard", patch)
}
func (c *Client) patchResource(ctx context.Context, target, component, media string, patch map[string]json.RawMessage) (Resource, error) {
	if target == "" || len(patch) == 0 {
		return Resource{}, badPatch()
	}
	allowed := "summary description location start end all_day time_zone"
	if component == "VCARD" {
		allowed = "formatted_name given_name family_name emails phones organization note"
	}
	for key, v := range patch {
		if !slices.Contains(strings.Fields(allowed), key) {
			return Resource{}, badPatch()
		}
		if key == "emails" || key == "phones" {
			var a []string
			if json.Unmarshal(v, &a) != nil {
				return Resource{}, badPatch()
			}
			for _, s := range a {
				if !validPatchText(s) {
					return Resource{}, badPatch()
				}
			}
		} else if key == "all_day" {
			var b bool
			if strings.TrimSpace(string(v)) == "null" || json.Unmarshal(v, &b) != nil {
				return Resource{}, badPatch()
			}
		} else {
			var s string
			if json.Unmarshal(v, &s) != nil || !validPatchText(s) {
				return Resource{}, badPatch()
			}
			if (key == "summary" || key == "formatted_name" || key == "start" || key == "end") && strings.TrimSpace(s) == "" {
				return Resource{}, badPatch()
			}
		}
	}
	// Reuse strict resource metadata verification and require the GET version to match.
	target, etag, err := c.verifiedDeleteTarget(ctx, target, media)
	if err != nil {
		return Resource{}, err
	}
	// Both GET and PUT stay on the positively verified URL.
	local := *c
	h := *c.HTTP
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return badPatch() }
	local.HTTP = &h
	current, err := local.get(ctx, target)
	if err != nil {
		return Resource{}, err
	}
	if !strongETag(current.ETag) || current.ETag != etag {
		return Resource{}, output.Validation("patch_version_changed", "resource changed during patch; fetch and retry", nil)
	}
	data, err := patchContent(current.Data, component, patch)
	if err != nil {
		return Resource{}, err
	}
	return local.put(ctx, target, media+"; charset=utf-8", data, false, etag)
}
func patchContent(data, component string, patch map[string]json.RawMessage) (string, error) {
	lines, err := parseContent(data, component)
	if err != nil {
		return "", err
	}
	depth := 1
	if component == "VEVENT" {
		depth = 2
	}
	// Select exactly one master component; detached recurrence instances remain verbatim.
	first, last := -1, -1
	for i, l := range lines {
		if l.name != "BEGIN" || !strings.EqualFold(l.value, component) || l.depth != depth-1 {
			continue
		}
		end := i + 1
		override := false
		for ; end < len(lines); end++ {
			if lines[end].depth == depth && lines[end].name == "RECURRENCE-ID" {
				override = true
			}
			if lines[end].name == "END" && lines[end].depth == depth && strings.EqualFold(lines[end].value, component) {
				break
			}
		}
		if end == len(lines) {
			return "", badPatch()
		}
		if !override {
			if first >= 0 {
				return "", badPatch()
			}
			first, last = i, end
		}
	}
	if first < 0 {
		return "", badPatch()
	}
	props := map[string][]contentLine{}
	for i, l := range lines {
		if i > first && i < last && l.depth == depth {
			props[l.name] = append(props[l.name], l)
		}
	}
	if len(props["UID"]) != 1 || props["UID"][0].value == "" {
		return "", badPatch()
	}
	changes := map[string][]string{}
	text := func(key string) string { var s string; _ = json.Unmarshal(patch[key], &s); return s }
	mappings := map[string]string{"summary": "SUMMARY", "description": "DESCRIPTION", "location": "LOCATION"}
	if component == "VCARD" {
		mappings = map[string]string{"formatted_name": "FN", "organization": "ORG", "note": "NOTE"}
	}
	for key, prop := range mappings {
		if _, ok := patch[key]; ok {
			changes[prop] = nil
			if s := text(key); s != "" {
				changes[prop] = []string{prop + ":" + escapedText(s)}
			}
		}
	}
	if component == "VCARD" {
		_, g := patch["given_name"]
		_, f := patch["family_name"]
		if g || f {
			if len(props["N"]) > 1 {
				return "", badPatch()
			}
			parts := []string{"", "", "", "", ""}
			head := "N"
			if len(props["N"]) == 1 {
				parts = splitText(props["N"][0].value, ';')
				head = props["N"][0].head
				if len(parts) != 5 {
					return "", badPatch()
				}
			}
			if g {
				parts[1] = escapedText(text("given_name"))
			}
			if f {
				parts[0] = escapedText(text("family_name"))
			}
			changes["N"] = []string{head + ":" + strings.Join(parts, ";")}
		}
		for key, prop := range map[string]string{"emails": "EMAIL", "phones": "TEL"} {
			if raw, ok := patch[key]; ok {
				var values []string
				if json.Unmarshal(raw, &values) != nil {
					return "", badPatch()
				}
				changes[prop] = nil
				for _, v := range values {
					if strings.TrimSpace(v) == "" {
						return "", badPatch()
					}
					changes[prop] = append(changes[prop], prop+":"+escapedText(v))
				}
			}
		}
	} else {
		_, a := patch["start"]
		_, b := patch["end"]
		_, d := patch["all_day"]
		_, z := patch["time_zone"]
		if a || b || d || z {
			hasExceptions := false
			for _, line := range lines {
				if line.name == "RECURRENCE-ID" {
					hasExceptions = true
				}
			}
			if hasExceptions || len(props["RRULE"])+len(props["RDATE"])+len(props["EXDATE"]) > 0 {
				return "", output.Validation("recurring_time_patch", "temporal patches of recurring events require recurrence-aware editing; metadata patches remain supported", nil)
			}
			if (d || z) && (!a || !b) {
				return "", output.Validation("patch_time_pair", "changing all_day or time_zone requires both start and end", nil)
			}
			all, zone := false, ""
			if len(props["DTSTART"]) == 1 {
				all = strings.EqualFold(parameter(props["DTSTART"][0].head, "VALUE"), "DATE")
				zone = parameter(props["DTSTART"][0].head, "TZID")
			}
			if d {
				_ = json.Unmarshal(patch["all_day"], &all)
			}
			if z {
				zone = text("time_zone")
			}
			values := []string{text("start"), text("end")}
			for i, name := range []string{"DTSTART", "DTEND"} {
				if (i == 0 && a) || (i == 1 && b) {
					continue
				}
				if len(props[name]) != 1 {
					return "", output.Validation("patch_time_pair", "existing event time cannot be resolved; provide both start and end", nil)
				}
				line := props[name][0]
				if parameter(line.head, "TZID") != zone || strings.EqualFold(parameter(line.head, "VALUE"), "DATE") != all {
					return "", badPatch()
				}
				layout, display := "20060102T150405Z", time.RFC3339
				if all {
					layout, display = "20060102", "2006-01-02"
				} else if zone != "" {
					layout, display = "20060102T150405", "2006-01-02T15:04:05"
				}
				t, e := time.Parse(layout, line.value)
				if e != nil {
					return "", badPatch()
				}
				values[i] = t.Format(display)
			}
			times, e := EventTimes(values[0], values[1], all, zone)
			if e != nil {
				return "", e
			}
			if a {
				changes["DTSTART"] = times[:1]
			}
			if b {
				changes["DTEND"] = times[1:]
			}
			if b {
				changes["DURATION"] = nil
			}
		}
	}
	var out strings.Builder
	written := map[string]bool{}
	for i, l := range lines {
		if i > first && i <= last && l.depth == depth {
			if replacements, ok := changes[l.name]; ok {
				if !written[l.name] {
					for _, v := range replacements {
						out.WriteString(v + "\r\n")
					}
					written[l.name] = true
				}
				continue
			}
			if l.name == "END" && strings.EqualFold(l.value, component) {
				for _, name := range []string{"SUMMARY", "DESCRIPTION", "LOCATION", "DTSTART", "DTEND", "FN", "N", "EMAIL", "TEL", "ORG", "NOTE"} {
					if !written[name] {
						for _, v := range changes[name] {
							out.WriteString(v + "\r\n")
						}
					}
				}
			}
		}
		out.WriteString(l.raw)
	}
	return out.String(), nil
}

func parameter(head, key string) string {
	for _, part := range strings.Split(head, ";")[1:] {
		pair := strings.SplitN(part, "=", 2)
		if len(pair) == 2 && strings.EqualFold(pair[0], key) {
			return strings.Trim(pair[1], "\"")
		}
	}
	return ""
}

func validPatchText(s string) bool {
	for _, r := range s {
		if r < 32 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}
