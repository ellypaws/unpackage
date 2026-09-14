package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Guard individual JSON tokens without buffering an entire Activity record.
type guarded struct {
	ctx            context.Context
	r              io.Reader
	bytes          int64
	quoted, escape bool
	run            int
	csv            bool
	recordBytes    int
}

func (g *guarded) Read(p []byte) (int, error) {
	if e := g.ctx.Err(); e != nil {
		return 0, e
	}
	n, e := g.r.Read(p)
	g.bytes += int64(n)
	for _, b := range p[:n] {
		if g.csv {
			g.recordBytes++
			if b == '\n' && !g.quoted {
				g.recordBytes = 0
			}
			if g.recordBytes > 8<<20 {
				return 0, fmt.Errorf("CSV record exceeds 8 MiB limit")
			}
		}
		if g.quoted {
			g.run++
			if g.escape {
				g.escape = false
			} else if b == '\\' {
				g.escape = true
			} else if b == '"' {
				g.quoted = false
				g.run = 0
			}
		} else {
			if b == '"' {
				g.quoted = true
				g.run = 0
			} else if b == ' ' || b == '\n' || b == '\r' || b == '\t' || b == ',' || b == ':' || b == '[' || b == ']' || b == '{' || b == '}' {
				g.run = 0
			} else {
				g.run++
			}
		}
		if g.run > 4<<20 {
			return 0, fmt.Errorf("JSON scalar exceeds 4 MiB limit")
		}
	}
	return n, e
}
func scalar(t json.Token) string {
	switch v := t.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}
func digits(v string) bool {
	if v == "" {
		return false
	}
	n, e := strconv.ParseUint(v, 10, 64)
	if e != nil || n == 0 {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var wanted = map[string]bool{"id": true, "channel_id": true, "channel_name": true, "guild_id": true, "guild_name": true, "name": true, "username": true, "type": true, "content": true, "contents": true, "timestamp": true, "guild": true, "attachments": true, "content_type": true, "filename": true, "url": true, "event_type": true, "event_id": true, "application_name": true, "application_id": true, "activity_duration_s": true, "total_duration_s": true, "duration": true, "duration_connected_ms": true, "os": true, "browser": true, "emoji_name": true, "context": true, "global_name": true, "channel": true, "channel_type": true, "server": true, "message_id": true, "length": true, "word_count": true, "num_urls": true, "num_attachments": true, "attachment_content_types": true, "attachment_mimetypes": true, "private": true, "recipient_ids": true}

const attachmentURLsKey = "attachment_urls"

func attachmentPresent(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value != "" && value != "[]" && value != "null" && value != "none"
}

func attachmentMedia(values ...string) bool {
	for _, value := range values {
		value = strings.ToLower(value)
		if strings.HasPrefix(value, "image/") || strings.HasPrefix(value, "video/") || strings.HasPrefix(value, "audio/") {
			return true
		}
		for _, extension := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".bmp", ".svg", ".mp4", ".webm", ".mov", ".mkv", ".avi", ".mp3", ".wav", ".ogg", ".flac", ".m4a"} {
			if strings.Contains(value, extension) {
				return true
			}
		}
	}
	return false
}

func attachmentURLs(values ...string) []string {
	var urls []string
	for _, value := range values {
		for field := range strings.FieldsSeq(value) {
			parsed, err := url.Parse(field)
			if err != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" || slices.Contains(urls, field) {
				continue
			}
			urls = append(urls, field)
		}
	}
	return urls
}

func mergeAttachmentURLs(current string, values ...string) string {
	return strings.Join(attachmentURLs(append([]string{current}, values...)...), "\n")
}

func walk(d *json.Decoder, depth int, object func(map[string]string) error, field func(string, string) error) (map[string]string, error) {
	if depth > 96 {
		return nil, fmt.Errorf("JSON nesting exceeds 96 levels")
	}
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return map[string]string{"$": scalar(t)}, nil
	}
	out := map[string]string{}
	switch delim {
	case '{':
		for d.More() {
			out["nonempty"] = "1"
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("invalid object key")
			}
			key = strings.ToLower(key)
			child, e := walk(d, depth+1, object, field)
			if e != nil {
				return nil, e
			}
			if child["nonempty"] == "1" || child["id"] != "" || child["question"] != "" {
				feature := map[string]string{"embeds": "embed", "poll": "poll", "sticker_items": "sticker", "stickers": "sticker", "message_snapshots": "forward"}[key]
				if feature != "" {
					out["has_"+feature] = "1"
				}
			}
			if v, ok := child["$"]; ok {
				if field != nil {
					if e = field(key, v); e != nil {
						return nil, e
					}
				}
				if wanted[key] {
					out[key] = v
				}
				if key == "attachments" && attachmentPresent(v) {
					out["has_attachments"] = "1"
					out[attachmentURLsKey] = mergeAttachmentURLs(out[attachmentURLsKey], v)
					if attachmentMedia(v) {
						out["has_media"] = "1"
					}
				}
			} else if key == "attachments" {
				for _, kind := range []string{"image", "video", "sound"} {
					if child["has_"+kind] == "1" {
						out["has_"+kind] = "1"
					}
				}
				if child["nonempty"] == "1" || child["has_attachments"] == "1" {
					out["has_attachments"] = "1"
				}
				if child["has_media"] == "1" {
					out["has_media"] = "1"
				}
				out[attachmentURLsKey] = mergeAttachmentURLs(out[attachmentURLsKey], child[attachmentURLsKey])
			} else if key == "properties" || key == "data" {
				for k, v := range child {
					if out[k] == "" {
						out[k] = v
					}
				}
			} else if key == "guild" {
				out["guild_id"] = child["id"]
				out["guild_name"] = child["name"]
			} else if key == "recipients" {
				out["recipients"] = child["items"]
			} else if wanted[key] && child["items"] != "" {
				out[key] = child["items"]
			}
		}
		if _, e = d.Token(); e != nil {
			return nil, e
		}
		if attachmentMedia(out["content_type"], out["filename"], out["url"]) {
			out["has_media"] = "1"
		}
		for _, kind := range []string{"image", "video", "audio"} {
			if strings.HasPrefix(strings.ToLower(out["content_type"]), kind+"/") {
				if kind == "audio" {
					kind = "sound"
				}
				out["has_"+kind] = "1"
			}
		}
		if object != nil {
			if e = object(out); e != nil {
				return nil, e
			}
		}
	case '[':
		nonempty := false
		items := 0
		for d.More() {
			nonempty = true
			child, childErr := walk(d, depth+1, object, field)
			if childErr != nil {
				e = childErr
				return nil, e
			}
			value := child["$"]
			if value == "" {
				for _, key := range []string{"id", "global_name", "username", "name"} {
					if child[key] != "" {
						value = child[key]
						break
					}
				}
			}
			if value != "" && items < 128 && len(value) <= 256 {
				if items > 0 {
					out["items"] += "\n"
				}
				out["items"] += value
				items++
			}
			if scalarValue, ok := child["$"]; ok && attachmentPresent(scalarValue) {
				out["has_attachments"] = "1"
				out[attachmentURLsKey] = mergeAttachmentURLs(out[attachmentURLsKey], scalarValue)
				if attachmentMedia(scalarValue) {
					out["has_media"] = "1"
				}
			}
			if child["has_media"] == "1" {
				out["has_media"] = "1"
			}
			for _, kind := range []string{"image", "video", "sound"} {
				if child["has_"+kind] == "1" {
					out["has_"+kind] = "1"
				}
			}
			out[attachmentURLsKey] = mergeAttachmentURLs(out[attachmentURLsKey], child["url"], child[attachmentURLsKey])
		}
		if _, e = d.Token(); e != nil {
			return nil, e
		}
		if nonempty {
			out["nonempty"] = "1"
		}
	default:
		return nil, fmt.Errorf("unexpected delimiter")
	}
	return out, nil
}
func stream(g *guarded, object func(map[string]string) error, field func(string, string) error) error {
	d := json.NewDecoder(g)
	d.UseNumber()
	if _, e := walk(d, 0, object, field); e != nil {
		return e
	}
	_, e := d.Token()
	if e == io.EOF {
		return nil
	}
	if e == nil {
		return fmt.Errorf("trailing JSON data")
	}
	return e
}
