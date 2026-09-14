package session

import (
	"slices"
	"strings"
	"time"

	"github.com/ellypaws/unpackage/pkg/store"
)

type Suggestion struct {
	Value, Label, Detail string
	Cursor               int
}

var searchGuidance = map[string]string{
	"from": "People and conversation participants", "in": "Channel or conversation", "server": "Server", "mentions": "Mentions a user", "has": "Message contains", "type": "DM, group, or server channel", "before": "Before a date", "after": "After a date", "on": "On a date", "id": "Exact message ID", "regex": "Regular expression, (?i) ignores case",
}

func CompleteSearch(text string, cursor int, catalog store.SearchCatalog) []Suggestion {
	runes := []rune(text)
	cursor = min(max(cursor, 0), len(runes))
	q, _ := store.ParseSearch(text)
	current := store.SearchToken{Start: cursor, End: cursor}
	for _, t := range q.Tokens {
		if cursor >= t.Start && cursor <= t.End {
			current = t
			break
		}
	}
	partial := string(runes[current.Start:cursor])
	key, value, hasKey := strings.Cut(strings.TrimPrefix(partial, "-"), ":")
	key = strings.ToLower(key)
	var out []Suggestion
	add := func(replacement, label, detail string) {
		if current.Exclude {
			replacement = "-" + replacement
		}
		prefix := string(runes[:current.Start]) + replacement
		suffix := string(runes[current.End:])
		if !strings.HasSuffix(replacement, ":") && (suffix == "" || suffix[0] != ' ') {
			prefix += " "
		}
		out = append(out, Suggestion{Value: prefix + suffix, Label: label, Detail: detail, Cursor: len([]rune(prefix))})
	}
	if !hasKey || !slices.Contains(store.SearchKeys, key) {
		for _, k := range store.SearchKeys {
			if strings.HasPrefix(k, strings.TrimPrefix(partial, "-")) {
				add(k+":", k+":", searchGuidance[k])
			}
		}
		return out
	}
	value = strings.Trim(value, "\"'")
	match := func(v string) bool { return strings.Contains(strings.ToLower(v), strings.ToLower(value)) }
	var choices []store.SearchChoice
	switch key {
	case "from", "mentions":
		choices = catalog.Users
	case "in":
		choices = catalog.Channels
	case "server":
		choices = catalog.Servers
	case "has", "type":
		values := store.SearchHas
		if key == "type" {
			values = store.SearchTypes
		}
		for _, v := range values {
			if match(v) {
				detail := ""
				if slices.Contains([]string{"embed", "poll", "sticker", "forward"}, v) {
					detail = "When included in the export"
				}
				add(key+":"+v, v, detail)
			}
		}
	case "before", "after", "on":
		for _, date := range []string{time.Now().Format(time.DateOnly), time.Now().AddDate(0, 0, -7).Format(time.DateOnly), time.Now().AddDate(0, -1, 0).Format(time.DateOnly)} {
			if match(date) {
				add(key+":"+date, date, "YYYY-MM-DD")
			}
		}
	case "regex":
		if value == "" {
			add(`regex:"(?i)pattern"`, "(?i)pattern", "Go regex, quoted when it contains spaces")
		}
	}
	var servers []string
	for _, t := range q.Tokens {
		if t.Key == "server" && !t.Exclude {
			for _, g := range catalog.Servers {
				if strings.EqualFold(t.Value, g.ID) || strings.EqualFold(t.Value, g.Name) {
					servers = append(servers, g.ID)
				}
			}
		}
	}
	for _, choice := range choices {
		if len(out) >= 8 {
			break
		}
		if !match(choice.Name) && !match(choice.ID) {
			continue
		}
		if key == "in" && len(servers) > 0 && !slices.Contains(servers, choice.Guild) {
			continue
		}
		if key == "mentions" && strings.ContainsAny(choice.ID, " @#") {
			continue
		}
		detail := choice.Kind
		if key == "in" {
			detail = choice.Server
			if choice.Guild == "" {
				detail = "No server, " + choice.Kind
			}
		}
		if choice.Recent != "" {
			detail += "  " + store.LocalDate(choice.Recent)
		}
		add(key+":"+store.QuoteSearch(choice.ID), choice.Name, detail)
	}
	return out
}

func CompleteCommand(text string, catalog store.SearchCatalog, history []string) []string {
	var out []string
	add := func(value string) {
		if value != text && strings.HasPrefix(strings.ToLower(value), strings.ToLower(text)) && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	for _, line := range slices.Backward(history) {
		add(line)
	}
	command, arg, hasArg := strings.Cut(text, " ")
	if command == "search" && hasArg {
		for _, suggestion := range CompleteSearch(arg, len([]rune(arg)), catalog) {
			add("search " + suggestion.Value)
		}
	}
	if !hasArg {
		for line := range strings.SplitSeq(Help, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 0 && slices.Contains([]string{"open", "wait", "stop", "status", "mode", "servers", "select", "exclude", "dates", "margin", "last", "before", "search", "channel", "kind", "media", "clear", "list", "show", "stats", "days", "summary", "leaders", "heatmap", "request", "help", "quit"}, fields[0]) {
				add(fields[0] + " ")
			}
		}
	} else {
		values := map[string][]string{"open": {"older ", "newer "}, "mode": {"auto", "missing", "all", "older", "newer", "present", "new"}, "kind": {"guild", "dm", "group", "unknown-dm", "unknown", "conflict", "all"}, "media": {"all", "attachments", "media"}, "list": {"jsonl", "tsv"}, "dates": {"clear"}, "summary": {"all"}, "leaders": {"servers ", "channels ", "people ", "games ", "platforms ", "emoji "}, "heatmap": {"messages all", "voice-time all", "play-time all"}}
		for _, value := range values[command] {
			add(command + " " + value)
		}
		if command == "select" || command == "exclude" {
			add(command + " all")
			for _, g := range catalog.Servers {
				add(command + " " + g.ID)
			}
		}
		if command == "channel" {
			for _, c := range catalog.Channels {
				add(command + " " + c.ID)
			}
		}
	}
	return out
}
