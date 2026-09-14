package store

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Metric int

const (
	MetricMessages Metric = iota
	MetricMissing
	MetricMedia
	MetricVoiceSessions
	MetricVoiceTime
	MetricPlayTime
	MetricSessions
	MetricReactions
	MetricStreams
	MetricAttachments
	MetricWords
	MetricLinks
	MetricEdits
	MetricDeletions
	MetricCount
)

var metricLabels = [MetricCount]string{"Messages", "Missing messages", "Media", "Voice sessions", "Voice time", "Play time", "App sessions", "Reactions", "Streams", "Attachments", "Words", "Links", "Edits", "Deletions"}
var metricKeys = [MetricCount]string{"messages", "missing", "media", "voice", "voice-time", "play-time", "sessions", "reactions", "streams", "attachments", "words", "links", "edits", "deletions"}

func (m Metric) Label() string { return metricLabels[m] }
func (m Metric) Key() string   { return metricKeys[m] }

// Duration metrics hold seconds instead of counts.
func (m Metric) Duration() bool { return m == MetricVoiceTime || m == MetricPlayTime }

func ParseMetric(key string) (Metric, bool) {
	i := slices.Index(metricKeys[:], strings.ToLower(key))
	return Metric(i), i >= 0
}

type Entity string

const (
	EntityServers   Entity = "servers"
	EntityChannels  Entity = "channels"
	EntityPeople    Entity = "people"
	EntityGames     Entity = "games"
	EntityPlatforms Entity = "platforms"
	EntityEmoji     Entity = "emoji"
)

var Entities = []Entity{EntityServers, EntityChannels, EntityPeople, EntityGames, EntityPlatforms, EntityEmoji}

// Metrics lists the metrics that can be non-zero for the entity, first one is its default order.
func (e Entity) Metrics() []Metric {
	switch e {
	case EntityGames:
		return []Metric{MetricPlayTime, MetricSessions}
	case EntityPlatforms:
		return []Metric{MetricSessions, MetricVoiceSessions, MetricVoiceTime}
	case EntityEmoji:
		return []Metric{MetricReactions}
	case EntityPeople:
		return []Metric{MetricMessages, MetricMissing, MetricMedia, MetricAttachments, MetricWords, MetricLinks, MetricVoiceSessions, MetricVoiceTime, MetricReactions, MetricEdits, MetricDeletions}
	}
	return []Metric{MetricMessages, MetricMissing, MetricMedia, MetricAttachments, MetricWords, MetricLinks, MetricVoiceSessions, MetricVoiceTime, MetricReactions, MetricStreams, MetricEdits, MetricDeletions}
}

// StatsFilter narrows statistics to a time range and, for drill-down, to one channel, game, platform, or emoji.
type StatsFilter struct {
	From, Until    time.Time
	Guilds         []string
	ExcludedGuilds []string
	Channel        string
	Game           string
	Platform       string
	Emoji          string
}

func (f StatsFilter) admitsMessages() bool { return f.Game == "" && f.Platform == "" && f.Emoji == "" }

func (f StatsFilter) admitsEvent(e Event) bool {
	if f.Channel != "" && e.Channel != f.Channel {
		return false
	}
	if f.Game != "" && (e.Kind != EventGame || e.Name != f.Game) {
		return false
	}
	if f.Platform != "" && e.Platform != f.Platform {
		return false
	}
	if f.Emoji != "" && (e.Kind != EventReaction || e.Name != f.Emoji) {
		return false
	}
	return true
}

type Leader struct {
	ID, Name, Kind string
	Parent         string
	ParentName     string
	Fallback       bool
	ParentFallback bool
	Values         [MetricCount]int
	Lifetime       int
}

type Stats struct {
	From, Until   time.Time
	First, Last   time.Time
	FirstMessage  time.Time
	Characters    int
	ActiveDays    int
	LongestStreak int
	LongestVoice  int
	Totals        [MetricCount]int
	Weekly        [MetricCount][7][24]int
	Daily         [MetricCount]map[string]int
	Monthly       [MetricCount]map[string]int
	Joined        int
	Servers       []Leader
	Channels      []Leader
	People        []Leader
	Games         []Leader
	Platforms     []Leader
	Emoji         []Leader
}

func (st *Stats) Leaders(entity Entity) []Leader {
	switch entity {
	case EntityChannels:
		return st.Channels
	case EntityPeople:
		return st.People
	case EntityGames:
		return st.Games
	case EntityPlatforms:
		return st.Platforms
	case EntityEmoji:
		return st.Emoji
	}
	return st.Servers
}

func SortLeaders(leaders []Leader, by Metric) {
	slices.SortFunc(leaders, func(a, b Leader) int {
		return cmp.Or(cmp.Compare(b.Values[by], a.Values[by]), strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.ID, b.ID))
	})
}

type board map[string]*Leader

func (b board) get(id, name, kind string, fallback bool) *Leader {
	if l := b[id]; l != nil {
		return l
	}
	l := &Leader{ID: id, Name: name, Kind: kind, Fallback: fallback}
	b[id] = l
	return l
}

func (b board) list() []Leader {
	out := make([]Leader, 0, len(b))
	for _, l := range b {
		out = append(out, *l)
	}
	return out
}

func (f StatsFilter) contains(t time.Time) bool {
	return (f.From.IsZero() || !t.Before(f.From)) && (f.Until.IsZero() || t.Before(f.Until))
}

func SnowflakeTime(id string) (time.Time, bool) {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil || n == 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(int64(n>>22) + 1420070400000).In(time.Local), true
}

func (st *Stats) add(metric Metric, t time.Time, n int) {
	if n == 0 {
		return
	}
	st.Totals[metric] += n
	st.Weekly[metric][(int(t.Weekday())+6)%7][t.Hour()] += n
	st.Daily[metric][t.Format(time.DateOnly)] += n
	st.Monthly[metric][t.Format("2006-01")] += n
}

func (st *Stats) observe(t time.Time) {
	if st.First.IsZero() || t.Before(st.First) {
		st.First = t
	}
	if t.After(st.Last) {
		st.Last = t
	}
}

type span struct{ start, end time.Time }

func newSpan(end time.Time, d time.Duration) (span, bool) {
	if d <= 0 {
		return span{}, false
	}
	return span{end.Add(-min(d, 7*24*time.Hour)), end}, true
}

// mergeSpans unions overlapping intervals so concurrent clients and reconnects never count twice.
func mergeSpans(spans []span) []span {
	slices.SortFunc(spans, func(a, b span) int { return a.start.Compare(b.start) })
	merged := spans[:0]
	for _, s := range spans {
		if n := len(merged); n > 0 && !s.start.After(merged[n-1].end) {
			if s.end.After(merged[n-1].end) {
				merged[n-1].end = s.end
			}
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

// spread splits a span across the clock hours it covers so peak-hour grids credit each hour with the seconds it received.
func spread(s span, fn func(t time.Time, seconds int)) {
	cursor := s.start
	for cursor.Before(s.end) {
		next := cursor.Truncate(time.Hour).Add(time.Hour)
		if next.After(s.end) {
			next = s.end
		}
		seconds := int(next.Sub(cursor).Round(time.Second) / time.Second)
		if seconds > 0 {
			fn(cursor, seconds)
		}
		cursor = next
	}
}

// credit adds merged spans to the grid for a metric and to each leader that owns them; it returns the longest span inside the range.
func (st *Stats) credit(f StatsFilter, metric Metric, all []span, owned map[*Leader][]span) int {
	longest := 0
	for _, s := range mergeSpans(all) {
		if f.contains(s.start) {
			longest = max(longest, int(s.end.Sub(s.start)/time.Second))
		}
		spread(s, func(at time.Time, seconds int) {
			if f.contains(at) {
				st.add(metric, at, seconds)
			}
		})
	}
	for l, spans := range owned {
		for _, s := range mergeSpans(spans) {
			spread(s, func(at time.Time, seconds int) {
				if f.contains(at) {
					l.Values[metric] += seconds
				}
			})
		}
	}
	return longest
}

// streak returns the number of distinct active days and the longest run of consecutive days.
func streak(daily map[string]int) (int, int) {
	days := make([]string, 0, len(daily))
	for day, n := range daily {
		if n > 0 {
			days = append(days, day)
		}
	}
	slices.Sort(days)
	longest, run := 0, 0
	var previous time.Time
	for _, day := range days {
		t, err := time.Parse(time.DateOnly, day)
		if err != nil {
			continue
		}
		if run > 0 && t.Sub(previous) == 24*time.Hour {
			run++
		} else {
			run = 1
		}
		previous = t
		longest = max(longest, run)
	}
	return len(days), longest
}

func (s *Store) Stats(ctx context.Context, f StatsFilter) (*Stats, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	s.mu.RLock()
	view := &Store{}
	for slot := range 2 {
		if snapshot := s.snapshots[slot]; snapshot != nil {
			view.snapshots[slot] = new(*snapshot)
		}
		view.messages[slot] = maps.Clone(s.messages[slot])
		view.sent[slot] = maps.Clone(s.sent[slot])
		view.events[slot] = maps.Clone(s.events[slot])
		view.channels[slot] = maps.Clone(s.channels[slot])
		view.names[slot] = maps.Clone(s.names[slot])
		view.serverNames[slot] = maps.Clone(s.serverNames[slot])
	}
	s.mu.RUnlock()
	s = view
	a, b := s.snapshots[0], s.snapshots[1]
	same := sameOwner(a, b)
	comparable, _ := compatible(a, b)
	serverIndex := s.serverNameIndexLocked(same)
	channels := make(map[string]channel, len(s.channels[0])+len(s.channels[1]))
	for slot, cs := range s.channels {
		if !same && (a != nil && slot != 0 || a == nil && slot != 1) {
			continue
		}
		for id, c := range cs {
			channels[id] = merge(channels[id], c)
		}
	}
	for id, c := range channels {
		channels[id] = resolveChannelServer(c, serverIndex)
	}
	labels := s.serverLabelsLocked(slices.Collect(maps.Values(channels)), same)
	lookup := s.nameLookupLocked()
	owner := ""
	if a != nil {
		owner = a.Owner
	} else if b != nil {
		owner = b.Owner
	}
	for id, c := range channels {
		c = applyServerLabel(c, labels)
		if c.conflict {
			c.guild = ""
			c.kind = "conflict"
		}
		channels[id] = c
	}
	guilds := make(map[string]bool, len(f.Guilds))
	for _, g := range f.Guilds {
		guilds[g] = true
	}
	excludedGuilds := make(map[string]bool, len(f.ExcludedGuilds))
	for _, g := range f.ExcludedGuilds {
		excludedGuilds[g] = true
	}
	matchesGuild := func(guild string) bool {
		return (len(guilds) == 0 || guilds[guild]) && !excludedGuilds[guild]
	}
	st := &Stats{From: f.From, Until: f.Until}
	for i := range MetricCount {
		st.Daily[i] = map[string]int{}
		st.Monthly[i] = map[string]int{}
	}
	servers, chans, people, games, platforms, emoji := board{}, board{}, board{}, board{}, board{}, board{}
	serverName := func(guild string) displayValue {
		return serverDisplay(channel{guild: guild, server: labels[guild].name, kind: "guild"})
	}
	channelBoards := func(c channel, guild, id string) []*Leader {
		out := make([]*Leader, 0, 3)
		conversation := c.kind == "dm" || c.kind == "unknown-dm" || c.kind == "group"
		if conversation {
			if id != "" {
				display := channelDisplay(c, id, owner, lookup)
				out = append(out, people.get(id, display.text, c.kind, display.fallback))
			}
			return out
		}
		if guild != "" {
			display := serverName(guild)
			out = append(out, servers.get(guild, display.text, "server", display.fallback))
		}
		if id != "" {
			display := channelDisplay(c, id, owner, lookup)
			label := display.text
			parent := displayValue{}
			if guild != "" {
				parent = serverName(guild)
			}
			ch := chans.get(id, label, cmp.Or(c.kind, "unknown"), display.fallback)
			ch.Parent = guild
			ch.ParentName = parent.text
			ch.ParentFallback = parent.fallback
			out = append(out, ch)
		}
		return out
	}
	included := func(slot int) bool {
		if same {
			return true
		}
		return a != nil && slot == 0 || a == nil && slot == 1
	}
	step := 0
	tick := func() error {
		step++
		if step%2048 == 0 {
			return ctx.Err()
		}
		return nil
	}
	for slot := range 2 {
		if !included(slot) || !f.admitsMessages() {
			continue
		}
		for observed := range s.observedMessagesLocked(slot) {
			if e := tick(); e != nil {
				return nil, e
			}
			m := observed.Message
			if same && s.skipCombinedMessage(slot, m.ID) {
				continue
			}
			sent := observed.Sent
			if same {
				sent = mergeSentMessage(sent, s.sent[1-slot][m.ID])
			}
			if m.Channel == "" {
				m.Channel = sent.Channel
			}
			if f.Channel != "" && m.Channel != f.Channel {
				continue
			}
			c := merge(channels[m.Channel], sentChannel(sent))
			if !matchesGuild(c.guild) {
				continue
			}
			t, ok := SnowflakeTime(m.ID)
			if !ok {
				continue
			}
			st.observe(t)
			if !f.contains(t) {
				continue
			}
			_, inNewRecord := s.messages[1][m.ID]
			missing := comparable && slot == 0 && !inNewRecord
			if st.FirstMessage.IsZero() || t.Before(st.FirstMessage) {
				st.FirstMessage = t
			}
			words := 0
			for range strings.FieldsSeq(m.Content) {
				words++
			}
			words = max(words, sent.Words)
			links := sent.URLs > 0 || strings.Contains(m.Content, "http://") || strings.Contains(m.Content, "https://")
			st.Characters += max(len(m.Content), sent.Length)
			st.add(MetricMessages, t, 1)
			st.add(MetricWords, t, words)
			for _, l := range channelBoards(c, c.guild, m.Channel) {
				l.Values[MetricMessages]++
				l.Values[MetricWords] += words
				if missing {
					l.Values[MetricMissing]++
				}
				if m.HasMedia {
					l.Values[MetricMedia]++
				}
				if m.HasAttachments {
					l.Values[MetricAttachments]++
				}
				if links {
					l.Values[MetricLinks]++
				}
			}
			if missing {
				st.add(MetricMissing, t, 1)
			}
			if m.HasMedia {
				st.add(MetricMedia, t, 1)
			}
			if m.HasAttachments {
				st.add(MetricAttachments, t, 1)
			}
			if links {
				st.add(MetricLinks, t, 1)
			}
		}
	}
	var voiceAll, playAll []span
	voiceOwned, playOwned := map[*Leader][]span{}, map[*Leader][]span{}
	seen := make(map[string]bool, len(s.events[0])+len(s.events[1]))
	for slot, events := range s.events {
		if !included(slot) {
			continue
		}
		for id, e := range events {
			if err := tick(); err != nil {
				return nil, err
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			c := channels[e.Channel]
			guild := cmp.Or(c.guild, e.Guild)
			if c.kind == "dm" || c.kind == "unknown-dm" || c.kind == "group" {
				guild = ""
			}
			if !matchesGuild(guild) || !f.admitsEvent(e) {
				continue
			}
			t := e.Time.In(time.Local)
			st.observe(t)
			var platform *Leader
			if e.Platform != "" {
				platform = platforms.get(e.Platform, e.Platform, "platform", false)
			}
			switch e.Kind {
			case EventVoiceJoin:
				if !f.contains(t) {
					continue
				}
				st.add(MetricVoiceSessions, t, 1)
				for _, l := range channelBoards(c, guild, e.Channel) {
					l.Values[MetricVoiceSessions]++
				}
				if platform != nil {
					platform.Values[MetricVoiceSessions]++
				}
			case EventVoice:
				sp, ok := newSpan(t, e.Duration)
				if !ok {
					continue
				}
				voiceAll = append(voiceAll, sp)
				for _, l := range channelBoards(c, guild, e.Channel) {
					voiceOwned[l] = append(voiceOwned[l], sp)
				}
				if platform != nil {
					voiceOwned[platform] = append(voiceOwned[platform], sp)
				}
			case EventGame:
				if e.Name == "" {
					continue
				}
				game := games.get(e.Name, e.Name, "game", false)
				game.Lifetime = max(game.Lifetime, int(e.Total/time.Second))
				if f.contains(t) {
					game.Values[MetricSessions]++
				}
				if sp, ok := newSpan(t, e.Duration); ok {
					playAll = append(playAll, sp)
					playOwned[game] = append(playOwned[game], sp)
				}
			case EventSession:
				if !f.contains(t) {
					continue
				}
				st.add(MetricSessions, t, 1)
				if platform != nil {
					platform.Values[MetricSessions]++
				}
			case EventReaction:
				if !f.contains(t) {
					continue
				}
				st.add(MetricReactions, t, 1)
				for _, l := range channelBoards(c, guild, e.Channel) {
					l.Values[MetricReactions]++
				}
				if e.Name != "" {
					emoji.get(e.Name, e.Name, "emoji", false).Values[MetricReactions]++
				}
			case EventGuildJoin:
				if f.contains(t) {
					st.Joined++
				}
			case EventStream:
				if !f.contains(t) {
					continue
				}
				st.add(MetricStreams, t, 1)
				for _, l := range channelBoards(c, guild, e.Channel) {
					l.Values[MetricStreams]++
				}
			case EventEdit, EventDelete:
				if !f.contains(t) {
					continue
				}
				metric := MetricEdits
				if e.Kind == EventDelete {
					metric = MetricDeletions
				}
				st.add(metric, t, 1)
				for _, l := range channelBoards(c, guild, e.Channel) {
					l.Values[metric]++
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	st.LongestVoice = st.credit(f, MetricVoiceTime, voiceAll, voiceOwned)
	st.credit(f, MetricPlayTime, playAll, playOwned)
	st.ActiveDays, st.LongestStreak = streak(st.Daily[MetricMessages])
	st.Servers = servers.list()
	st.Channels = chans.list()
	st.People = people.list()
	st.Games = games.list()
	st.Platforms = platforms.list()
	st.Emoji = emoji.list()
	return st, ctx.Err()
}
