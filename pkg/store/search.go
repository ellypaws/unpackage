package store

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

type SearchToken struct {
	Key, Value string
	Start, End int
	Exclude    bool
}

var SearchKeys = []string{"from", "in", "server", "mentions", "has", "type", "before", "after", "on", "id", "regex"}
var SearchHas = []string{"image", "video", "sound", "link", "file", "embed", "poll", "sticker", "forward"}
var SearchTypes = []string{"dm", "group", "server", "thread", "unknown"}

type SearchQuery struct {
	Tokens   []SearchToken
	patterns map[int]*regexp.Regexp
	literal  map[int]string
}

func ParseSearch(text string) (SearchQuery, error) {
	q := SearchQuery{patterns: map[int]*regexp.Regexp{}, literal: map[int]string{}}
	runes := []rune(text)
	for i := 0; i < len(runes); {
		if unicode.IsSpace(runes[i]) {
			i++
			continue
		}
		t := SearchToken{Start: i}
		if runes[i] == '-' {
			t.Exclude = true
			i++
		}
		var value strings.Builder
		var quote rune
		for i < len(runes) {
			r := runes[i]
			if quote != 0 {
				if r == quote {
					quote = 0
					i++
					continue
				}
				if r == '\\' && i+1 < len(runes) && (runes[i+1] == quote || runes[i+1] == '\\') {
					i++
					r = runes[i]
				}
			} else {
				if unicode.IsSpace(r) {
					break
				}
				if (r == '"' || r == '\'') && value.Len() == 0 {
					quote = r
					i++
					continue
				}
				if r == ':' && t.Key == "" && slices.Contains(SearchKeys, strings.ToLower(value.String())) {
					t.Key = strings.ToLower(value.String())
					value.Reset()
					i++
					continue
				}
			}
			value.WriteRune(r)
			i++
		}
		t.End = i
		t.Value = value.String()
		q.Tokens = append(q.Tokens, t)
		if quote != 0 {
			return q, fmt.Errorf("Close the quote")
		}
		if t.Value == "" {
			return q, fmt.Errorf("Choose a value for %s:", t.Key)
		}
		switch t.Key {
		case "":
			q.literal[len(q.Tokens)-1] = strings.ToLower(t.Value)
		case "regex":
			pattern, err := regexp.Compile(t.Value)
			if err != nil {
				return q, fmt.Errorf("Invalid regex: %s", err)
			}
			q.patterns[len(q.Tokens)-1] = pattern
		case "has":
			if !slices.Contains(SearchHas, strings.ToLower(t.Value)) {
				return q, fmt.Errorf("Unknown has: value %q", t.Value)
			}
		case "type":
			if !slices.Contains(SearchTypes, strings.ToLower(t.Value)) {
				return q, fmt.Errorf("Use type:dm, group, server, thread, or unknown")
			}
		case "before", "after", "on":
			if _, err := time.Parse(time.DateOnly, t.Value); err != nil {
				return q, fmt.Errorf("Use YYYY-MM-DD for %s:", t.Key)
			}
		}
	}
	return q, nil
}

func QuoteSearch(value string) string {
	if strings.ContainsAny(value, " \t\n\"'\\") {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
	}
	return value
}

func searchEqual(value string, candidates ...string) bool {
	value = directParticipant(value)
	value = strings.TrimPrefix(strings.TrimPrefix(value, "@"), "#")
	return slices.ContainsFunc(candidates, func(candidate string) bool {
		candidate = directParticipant(candidate)
		return strings.EqualFold(value, strings.TrimPrefix(strings.TrimPrefix(candidate, "@"), "#"))
	})
}

func directParticipant(name string) string {
	if participant, ok := ConversationParticipant(name); ok {
		return participant
	}
	return strings.TrimSpace(name)
}

func identitySearchEqual(value string, person identity) bool {
	if searchEqual(value, person.username, person.globalName, identityDisplay(person)) {
		return true
	}
	for alias := range strings.SplitSeq(person.aliases, "\n") {
		if searchEqual(value, alias) {
			return true
		}
	}
	return false
}

// Repeated values form a set; distinct filters intersect. Exclusions always subtract.
func (q SearchQuery) matches(keys []string, match func(int, SearchToken) bool) bool {
	var needed, found uint16
	for i, t := range q.Tokens {
		key := slices.Index(keys, t.Key)
		if key < 0 {
			continue
		}
		ok := match(i, t)
		if t.Exclude {
			if ok {
				return false
			}
			continue
		}
		if t.Key == "" || t.Key == "regex" {
			if !ok {
				return false
			}
			continue
		}
		bit := uint16(1) << key
		needed |= bit
		if ok {
			found |= bit
		}
	}
	return needed == found
}

func (q SearchQuery) channelMatches(c channel, id, label, owner string, lookup func(string) string) bool {
	return q.matches([]string{"server", "in", "from", "type"}, func(_ int, t SearchToken) bool {
		switch t.Key {
		case "server":
			return !c.conflict && searchEqual(t.Value, c.guild, c.server)
		case "in":
			return searchEqual(t.Value, id, label, c.name, c.title)
		case "type":
			return strings.EqualFold(t.Value, channelCategory(c))
		case "from":
			if searchEqual(t.Value, owner, lookup(owner), "me") {
				return true
			}
			if c.kind != "dm" && c.kind != "unknown-dm" && c.kind != "group" {
				return false
			}
			for recipient := range strings.SplitSeq(c.recipients, "\n") {
				if recipient != "" && searchEqual(t.Value, recipient, lookup(recipient)) {
					return true
				}
			}
			if c.kind != "group" && c.recipients == "" && searchEqual(t.Value, id) {
				return true
			}
			return searchEqual(t.Value, c.name, label)
		}
		return false
	})
}

func (q SearchQuery) messageMatches(m Message) bool {
	content := ""
	if len(q.literal) > 0 {
		content = strings.ToLower(m.Content)
	}
	return q.matches([]string{"", "regex", "mentions", "has", "before", "after", "on", "id"}, func(i int, t SearchToken) bool {
		switch t.Key {
		case "":
			return strings.Contains(content, q.literal[i])
		case "regex":
			return q.patterns[i].MatchString(m.Content)
		case "id":
			return m.ID == t.Value
		case "mentions":
			return strings.Contains(m.Content, "<@"+strings.Trim(t.Value, "<@!>")+">") || strings.Contains(m.Content, "<@!"+strings.Trim(t.Value, "<@!>")+">")
		case "before":
			return LocalDate(m.Date) < t.Value
		case "after":
			return LocalDate(m.Date) > t.Value
		case "on":
			return LocalDate(m.Date) == t.Value
		case "has":
			return messageHas(m, strings.ToLower(t.Value))
		}
		return false
	})
}

func messageHas(m Message, kind string) bool {
	switch kind {
	case "file":
		return m.HasAttachments
	case "link":
		return m.HasLink || strings.Contains(m.Content, "https://") || strings.Contains(m.Content, "http://")
	case "embed", "poll", "sticker", "forward":
		return slices.Contains(m.Features, kind)
	}
	if slices.Contains(m.Features, kind) {
		return true
	}
	for _, raw := range m.AttachmentURLs {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		ext := strings.ToLower(path.Ext(u.Path))
		switch kind {
		case "image":
			if slices.Contains([]string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp", ".svg"}, ext) {
				return true
			}
		case "video":
			if slices.Contains([]string{".mp4", ".webm", ".mov", ".mkv", ".avi"}, ext) {
				return true
			}
		case "sound":
			if slices.Contains([]string{".mp3", ".wav", ".ogg", ".flac", ".m4a"}, ext) {
				return true
			}
		}
	}
	return false
}

type SearchChoice struct {
	ID, Name, Guild, Server, Kind, Recent string
	Fallback, ServerFallback              bool
}
type SearchCatalog struct {
	Channels, Users, Servers []SearchChoice
	Loading                  bool
}

func (s *Store) SearchCatalog(ctx context.Context) SearchCatalog {
	s.mu.RLock()
	var out SearchCatalog
	users, servers := map[string]SearchChoice{}, map[string]SearchChoice{}
	lookup := s.nameLookupLocked()
	same := sameOwner(s.snapshots[0], s.snapshots[1])
	serverIndex := s.serverNameIndexLocked(same)
	labels := s.serverLabelsLocked(s.effectiveChannelsLocked(same), same)
	channels := map[string]channel{}
	owner := ""
	if s.snapshots[0] != nil {
		owner = s.snapshots[0].Owner
	} else if s.snapshots[1] != nil {
		owner = s.snapshots[1].Owner
	}
	for slot, cs := range s.channels {
		if !same && (s.snapshots[0] != nil && slot != 0 || s.snapshots[0] == nil && slot != 1) {
			continue
		}
		for id, c := range cs {
			channels[id] = merge(channels[id], resolveChannelServer(c, serverIndex))
		}
		for id := range s.names[slot] {
			name := lookup(id)
			users[id] = SearchChoice{ID: id, Name: cmp.Or(name, id), Kind: "Known user", Fallback: name == ""}
		}
		if snapshot := s.snapshots[slot]; snapshot != nil {
			out.Loading = out.Loading || snapshot.State == "loading"
			if snapshot.Owner != "" {
				name := lookup(snapshot.Owner)
				users[snapshot.Owner] = SearchChoice{ID: snapshot.Owner, Name: cmp.Or(name, "me"), Kind: "Your messages", Fallback: name == ""}
			}
		}
	}
	for id, c := range channels {
		if ctx.Err() != nil {
			s.mu.RUnlock()
			return out
		}
		c = applyServerLabel(c, labels)
		recent := max(s.recent[0][id], s.recent[1][id])
		if recent == "" {
			continue
		}
		name := channelDisplay(c, id, owner, lookup)
		server := serverDisplay(c)
		guild := c.guild
		if c.conflict {
			guild = ""
			c.kind = "conflict"
		}
		out.Channels = append(out.Channels, SearchChoice{ID: id, Name: name.text, Guild: guild, Server: server.text, Kind: c.kind, Recent: recent, Fallback: name.fallback, ServerFallback: server.fallback})
		if guild != "" {
			servers[guild] = SearchChoice{ID: guild, Name: server.text, Recent: max(recent, servers[guild].Recent), Fallback: server.fallback}
		}
		if c.kind == "dm" || c.kind == "group" || c.kind == "unknown-dm" {
			for person := range strings.SplitSeq(c.recipients, "\n") {
				if person != "" {
					u := users[person]
					u.ID = person
					name := lookup(person)
					u.Name = cmp.Or(name, person)
					u.Fallback = name == ""
					u.Kind = "Conversation participant"
					u.Recent = max(u.Recent, recent)
					users[person] = u
				}
			}
			if c.kind != "group" && c.name != "" && c.recipients == "" {
				participant := directParticipant(c.name)
				users[id] = SearchChoice{ID: id, Name: directLabel(participant), Kind: "Conversation participant", Recent: recent, Fallback: unresolvedParticipant(participant)}
			}
		}
	}
	s.mu.RUnlock()
	for _, u := range users {
		out.Users = append(out.Users, u)
	}
	for _, g := range servers {
		out.Servers = append(out.Servers, g)
	}
	for _, items := range [][]SearchChoice{out.Channels, out.Users, out.Servers} {
		slices.SortFunc(items, func(a, b SearchChoice) int {
			return cmp.Or(strings.Compare(b.Recent, a.Recent), strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
		})
	}
	return out
}

func (c SearchCatalog) Validate(q SearchQuery) error {
	var servers []string
	for _, t := range q.Tokens {
		if t.Key == "server" && !t.Exclude {
			for _, g := range c.Servers {
				if searchEqual(t.Value, g.ID, g.Name) {
					servers = append(servers, g.ID)
				}
			}
		}
	}
	for _, t := range q.Tokens {
		var choices []SearchChoice
		switch t.Key {
		case "server":
			choices = c.Servers
		case "in":
			choices = c.Channels
		case "from", "mentions":
			choices = c.Users
		default:
			continue
		}
		if t.Key == "from" && t.Value == "me" {
			continue
		}
		if t.Key == "mentions" && digits(t.Value) {
			continue
		}
		matched := false
		related := false
		for _, v := range choices {
			if searchEqual(t.Value, v.ID, v.Name) {
				matched = true
				related = related || len(servers) == 0 || slices.Contains(servers, v.Guild)
			}
		}
		if !matched && !c.Loading {
			return fmt.Errorf("Unknown %s: %s", t.Key, t.Value)
		}
		if t.Key == "in" && matched && !related && !t.Exclude {
			return fmt.Errorf("Channel is not in the selected server")
		}
	}
	return nil
}
