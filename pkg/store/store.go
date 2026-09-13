package store

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Store struct {
	mu        sync.RWMutex
	snapshots [2]*Snapshot
	messages  [2]map[string]Message
	channels  [2]map[string]channel
	byDate    [2]map[string][]string
	events    [2]map[string]Event
	sent      [2]map[string]SentMessage
	names     [2]map[string]string
	interned  map[string]string
}

type EventKind uint8

const (
	EventVoiceJoin EventKind = iota + 1
	EventVoice
	EventGame
	EventSession
	EventReaction
	EventGuildJoin
	EventStream
	EventEdit
	EventDelete
)

type ActivitySource uint8

const (
	ActivityReporting ActivitySource = 1 << iota
	ActivityTNS
	ActivityOther
)

// Event is one analytics record kept for statistics; Duration is the voice or play time it reports.
type Event struct {
	ID              string
	Kind            EventKind
	Time            time.Time
	Guild, Channel  string
	Name, Platform  string
	Duration, Total time.Duration
}

// SentMessage is the metadata Discord retains for a send_message analytics event.
type SentMessage struct {
	ID, EventID, Channel, Guild string
	Time                        time.Time
	Platform                    string
	Length, Words               int
	URLs, Attachments           int
	HasMedia                    bool
	Sources                     ActivitySource
}
type Message struct {
	ID, Channel, Date, Content string
	AttachmentURLs             []string
	HasAttachments, HasMedia   bool
}
type ChannelObservation struct {
	ID, Name, Guild, Server, Kind string
	Title, Recipients             string
	Rank                          int
}
type Row struct {
	ID             string   `json:"message_id"`
	Channel        string   `json:"channel_id"`
	Date           string   `json:"date"`
	Content        string   `json:"content"`
	Guild          string   `json:"server_id"`
	Server         string   `json:"server"`
	Name           string   `json:"channel"`
	Kind           string   `json:"kind"`
	Status         string   `json:"status"`
	HasAttachments bool     `json:"has_attachments"`
	HasMedia       bool     `json:"has_media"`
	AttachmentURLs []string `json:"-"`
	MessageRecord  bool     `json:"message_record"`
	SendEvent      bool     `json:"send_message_event"`
	Sources        []string `json:"sources,omitempty"`
	SendEventID    string   `json:"send_message_event_id,omitempty"`
	SendTime       string   `json:"send_message_time,omitempty"`
	Platform       string   `json:"platform,omitempty"`
	ReportedLength int      `json:"reported_length"`
	ReportedWords  int      `json:"reported_words"`
	ReportedURLs   int      `json:"reported_urls"`
	ReportedFiles  int      `json:"reported_attachments"`
}
type Group struct {
	ID, Name string
	Count    int
	Missing  int
}
type Filter struct {
	Mode, Search, Channel, Kind, Media string
	Guilds, ExcludedGuilds, Dates      []string
	IncidentSeconds                    map[int64]int64
	From, Until                        string
	Limit, Offset                      int
	DateBefore, DateAfter              int
}
type Snapshot struct {
	Slot                      int
	Owner, State, Phase, Path string
	Complete                  bool
	Count, Bytes, Total       int64
	Errors                    int
}
type channel struct {
	name, guild, server, kind string
	title, recipients         string
	nameRank, guildRank       int
	serverRank, kindRank      int
	conflict                  bool
}

type serverLabel struct {
	name  string
	rank  int
	count int
}

func New() *Store { return &Store{} }
func (s *Store) Clear(slot int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[slot] = nil
	s.messages[slot] = nil
	s.channels[slot] = nil
	s.byDate[slot] = nil
	s.events[slot] = nil
	s.sent[slot] = nil
	s.names[slot] = nil
}
func (s *Store) Reset(ctx context.Context, slot int) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if slot < 0 || slot > 1 {
		return fmt.Errorf("choose older or newer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[slot] = &Snapshot{Slot: slot, State: "loading", Phase: "discovering"}
	s.messages[slot] = map[string]Message{}
	s.channels[slot] = map[string]channel{}
	s.byDate[slot] = map[string][]string{}
	s.events[slot] = map[string]Event{}
	s.sent[slot] = map[string]SentMessage{}
	s.names[slot] = map[string]string{}
	return nil
}

// Names records account display names keyed by user ID so group conversations can list their participants.
func (s *Store) Names(ctx context.Context, slot int, names map[string]string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.names[slot] == nil {
		return fmt.Errorf("slot is not open")
	}
	for id, name := range names {
		if len(name) > 256 || len(s.names[slot]) >= 1<<16 {
			continue
		}
		s.names[slot][id] = name
	}
	return nil
}

func (s *Store) nameLookupLocked() func(string) string {
	return func(id string) string {
		for _, names := range s.names {
			if name := names[id]; name != "" {
				return name
			}
		}
		return ""
	}
}

// channelLabel names a channel for display; group conversations list their participants by global name.
func channelLabel(c channel, id string, lookup func(string) string) string {
	if c.kind != "group" {
		return cmp.Or(c.name, id)
	}
	participants := c.name
	if participants == "" && c.recipients != "" {
		var names []string
		for recipient := range strings.SplitSeq(c.recipients, "\n") {
			if name := lookup(recipient); name != "" {
				names = append(names, name)
			} else if !digits(recipient) && recipient != "" {
				names = append(names, recipient)
			}
		}
		participants = strings.Join(names, ", ")
	}
	switch {
	case c.title != "" && participants != "":
		return c.title + " (" + participants + ")"
	case c.title != "":
		return c.title
	case participants != "":
		return participants
	}
	if n := strings.Count(c.recipients, "\n") + 1; c.recipients != "" && n > 1 {
		return fmt.Sprintf("Group of %d", n)
	}
	return id
}

func digits(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func (s *Store) Events(ctx context.Context, slot int, events []Event) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.events[slot] == nil {
		return fmt.Errorf("slot is not open")
	}
	if s.interned == nil {
		s.interned = map[string]string{}
	}
	for _, e := range events {
		if e.ID == "" || e.Kind == 0 {
			continue
		}
		e.Guild = s.intern(e.Guild)
		e.Channel = s.intern(e.Channel)
		e.Name = s.intern(e.Name)
		e.Platform = s.intern(e.Platform)
		s.events[slot][e.ID] = e
	}
	return nil
}

func sourceNames(sources ActivitySource, messageRecord bool) []string {
	n := 0
	if messageRecord {
		n++
	}
	for _, source := range []ActivitySource{ActivityReporting, ActivityTNS, ActivityOther} {
		if sources&source != 0 {
			n++
		}
	}
	labels := make([]string, 0, n)
	if messageRecord {
		labels = append(labels, "Messages")
	}
	if sources&ActivityReporting != 0 {
		labels = append(labels, "Activity/reporting")
	}
	if sources&ActivityTNS != 0 {
		labels = append(labels, "Activity/tns")
	}
	if sources&ActivityOther != 0 {
		labels = append(labels, "Activity")
	}
	return labels
}

func mergeText(a, b string) string {
	if a == "" || b != "" && strings.Compare(b, a) < 0 {
		return b
	}
	return a
}

func mergeDescription(a, b string) string {
	if len(b) > len(a) || len(b) == len(a) && strings.Compare(b, a) < 0 {
		return b
	}
	return a
}

func mergeSentMessage(a, b SentMessage) SentMessage {
	a.ID = cmp.Or(a.ID, b.ID)
	a.EventID = mergeText(a.EventID, b.EventID)
	a.Channel = mergeText(a.Channel, b.Channel)
	a.Guild = mergeText(a.Guild, b.Guild)
	a.Platform = mergeDescription(a.Platform, b.Platform)
	if a.Time.IsZero() || !b.Time.IsZero() && b.Time.Before(a.Time) {
		a.Time = b.Time
	}
	a.Length = max(a.Length, b.Length)
	a.Words = max(a.Words, b.Words)
	a.URLs = max(a.URLs, b.URLs)
	a.Attachments = max(a.Attachments, b.Attachments)
	a.HasMedia = a.HasMedia || b.HasMedia
	a.Sources |= b.Sources
	return a
}

// SentMessages merges repeated send_message records from Activity files by message ID.
func (s *Store) SentMessages(ctx context.Context, slot int, messages []SentMessage) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent[slot] == nil {
		return fmt.Errorf("slot is not open")
	}
	for _, message := range messages {
		if !digits(message.ID) {
			continue
		}
		if s.interned == nil {
			s.interned = map[string]string{}
		}
		message.Channel = s.intern(message.Channel)
		message.Guild = s.intern(message.Guild)
		message.Platform = s.intern(message.Platform)
		_, alreadySent := s.sent[slot][message.ID]
		s.sent[slot][message.ID] = mergeSentMessage(s.sent[slot][message.ID], message)
		if _, hasRecord := s.messages[slot][message.ID]; !hasRecord && !alreadySent {
			day := LocalDate(Day(message.ID))
			s.byDate[slot][day] = append(s.byDate[slot][day], message.ID)
		}
	}
	if snapshot := s.snapshots[slot]; snapshot != nil {
		snapshot.Count = int64(s.messageCountLocked(slot))
	}
	return nil
}

// Analytics files repeat the same guild, channel, and application strings millions of times.
func (s *Store) intern(v string) string {
	if v == "" {
		return ""
	}
	if existing, ok := s.interned[v]; ok {
		return existing
	}
	if len(s.interned) < 1<<20 {
		s.interned[v] = v
	}
	return v
}
func (s *Store) SetSnapshot(slot int, v Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v.Slot = slot
	v.Count = int64(s.messageCountLocked(slot))
	s.snapshots[slot] = &v
}

func (s *Store) messageCountLocked(slot int) int {
	count := len(s.messages[slot])
	for id := range s.sent[slot] {
		if _, ok := s.messages[slot][id]; !ok {
			count++
		}
	}
	return count
}

func (s *Store) Add(ctx context.Context, slot int, ms []Message) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]bool, len(ms))
	for _, m := range ms {
		if _, exists := s.messages[slot][m.ID]; exists || seen[m.ID] {
			return fmt.Errorf("duplicate message ID")
		}
		seen[m.ID] = true
	}
	for _, m := range ms {
		s.messages[slot][m.ID] = m
		if _, indexed := s.sent[slot][m.ID]; !indexed {
			day := LocalDate(m.Date)
			s.byDate[slot][day] = append(s.byDate[slot][day], m.ID)
		}
	}
	s.snapshots[slot].Count = int64(s.messageCountLocked(slot))
	return nil
}
func merge(a, b channel) channel {
	a.conflict = a.conflict || b.conflict || (a.guild != "" && b.guild != "" && a.guild != b.guild)
	if preferField(a.name, a.nameRank, b.name, b.nameRank) {
		a.name = b.name
		a.nameRank = b.nameRank
	}
	if preferField(a.guild, a.guildRank, b.guild, b.guildRank) {
		a.guild = b.guild
		a.guildRank = b.guildRank
	}
	if preferField(a.server, a.serverRank, b.server, b.serverRank) {
		a.server = b.server
		a.serverRank = b.serverRank
	}
	a.title = cmp.Or(a.title, b.title)
	if len(b.recipients) > len(a.recipients) {
		a.recipients = b.recipients
	}
	if a.kind == "dm" && b.kind == "unknown-dm" || a.kind == "unknown-dm" && b.kind == "dm" {
		a.kind = "unknown-dm"
		a.kindRank = max(a.kindRank, b.kindRank)
	} else if b.kind != "unknown" && preferField(a.kind, a.kindRank, b.kind, b.kindRank) {
		a.kind = b.kind
		a.kindRank = b.kindRank
	}
	return a
}

func preferField(current string, currentRank int, candidate string, candidateRank int) bool {
	return candidate != "" && (current == "" || candidateRank > currentRank || candidateRank == currentRank && strings.Compare(candidate, current) < 0)
}

func channelObservation(name, guild, server, kind, title, recipients string, rank int) channel {
	c := channel{name: name, guild: guild, server: server, kind: kind, title: title, recipients: recipients}
	if name != "" {
		c.nameRank = rank
	}
	if guild != "" {
		c.guildRank = rank
	}
	if server != "" {
		c.serverRank = rank
	}
	if kind != "" {
		c.kindRank = rank
	}
	return c
}

func sameOwner(a, b *Snapshot) bool {
	return a != nil && b != nil && a.Owner != "" && a.Owner == b.Owner
}

func (s *Store) effectiveChannelsLocked(combine bool) []channel {
	if !combine {
		out := make([]channel, 0, len(s.channels[0])+len(s.channels[1]))
		for _, channels := range s.channels {
			out = append(out, slices.Collect(maps.Values(channels))...)
		}
		return out
	}
	byID := make(map[string]channel, len(s.channels[0])+len(s.channels[1]))
	for _, channels := range s.channels {
		for id, c := range channels {
			byID[id] = merge(byID[id], c)
		}
	}
	return slices.Collect(maps.Values(byID))
}

func serverLabels(channels []channel) map[string]serverLabel {
	candidates := map[string]map[string]serverLabel{}
	for _, c := range channels {
		if c.guild == "" || c.server == "" || c.conflict {
			continue
		}
		if candidates[c.guild] == nil {
			candidates[c.guild] = map[string]serverLabel{}
		}
		label := candidates[c.guild][c.server]
		label.name = c.server
		label.rank = max(label.rank, c.serverRank)
		label.count++
		candidates[c.guild][c.server] = label
	}
	labels := make(map[string]serverLabel, len(candidates))
	for guild, names := range candidates {
		for _, candidate := range names {
			current := labels[guild]
			if candidate.rank > current.rank || candidate.rank == current.rank && (candidate.count > current.count || candidate.count == current.count && strings.Compare(candidate.name, current.name) < 0) {
				labels[guild] = candidate
			}
		}
	}
	return labels
}

func applyServerLabel(c channel, labels map[string]serverLabel) channel {
	if label := labels[c.guild]; !c.conflict && label.name != "" {
		c.server = label.name
		c.serverRank = label.rank
	}
	return c
}
func (s *Store) Channel(ctx context.Context, slot int, id, name, guild, server, kind string, rank int) error {
	return s.Channels(ctx, slot, []ChannelObservation{{ID: id, Name: name, Guild: guild, Server: server, Kind: kind, Rank: rank}})
}
func (s *Store) Channels(ctx context.Context, slot int, observations []ChannelObservation) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	for _, observation := range observations {
		if len(observation.Name) > 4096 || len(observation.Server) > 4096 || len(observation.Title) > 4096 || len(observation.Recipients) > 4096 {
			return fmt.Errorf("channel name too long")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, observation := range observations {
		if observation.ID == "" {
			continue
		}
		s.channels[slot][observation.ID] = merge(s.channels[slot][observation.ID], channelObservation(observation.Name, observation.Guild, observation.Server, observation.Kind, observation.Title, observation.Recipients, observation.Rank))
	}
	return nil
}
func (s *Store) Snapshots(ctx context.Context) ([]Snapshot, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Snapshot
	for _, v := range s.snapshots {
		if v != nil {
			out = append(out, *v)
		}
	}
	return out, nil
}
func compatible(a, b *Snapshot) (bool, string) {
	if a == nil || b == nil {
		return false, ""
	}
	if a.Owner == "" || b.Owner == "" {
		return false, "Account could not be verified"
	}
	if a.Owner != b.Owner {
		return false, "These packages belong to different accounts"
	}
	if !a.Complete || !b.Complete {
		return false, "Comparison pending"
	}
	return true, ""
}
func (s *Store) Compatible(ctx context.Context) (bool, string) {
	if e := ctx.Err(); e != nil {
		return false, e.Error()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return compatible(s.snapshots[0], s.snapshots[1])
}

type observedMessage struct {
	Message
	Sent          SentMessage
	MessageRecord bool
}

func (s *Store) observedMessageLocked(slot int, id string) (observedMessage, bool) {
	m, messageRecord := s.messages[slot][id]
	sent, sendEvent := s.sent[slot][id]
	if !messageRecord && !sendEvent {
		return observedMessage{}, false
	}
	if !messageRecord {
		m = Message{ID: id, Channel: sent.Channel, Date: Day(id), HasAttachments: sent.Attachments > 0, HasMedia: sent.HasMedia}
	} else {
		m.HasAttachments = m.HasAttachments || sent.Attachments > 0
		m.HasMedia = m.HasMedia || sent.HasMedia
	}
	return observedMessage{Message: m, Sent: sent, MessageRecord: messageRecord}, true
}

func (s *Store) observedMessagesLocked(slot int) iter.Seq[observedMessage] {
	return func(yield func(observedMessage) bool) {
		for id := range s.messages[slot] {
			message, _ := s.observedMessageLocked(slot, id)
			if !yield(message) {
				return
			}
		}
		for id := range s.sent[slot] {
			if _, ok := s.messages[slot][id]; ok {
				continue
			}
			message, _ := s.observedMessageLocked(slot, id)
			if !yield(message) {
				return
			}
		}
	}
}

func (s *Store) hasObservedMessageLocked(slot int, id string) bool {
	_, inMessages := s.messages[slot][id]
	_, inActivity := s.sent[slot][id]
	return inMessages || inActivity
}

// skipCombinedMessage chooses one copy for an account represented by two packages.
// A Messages record wins over analytics-only evidence, then the newer copy wins.
func (s *Store) skipCombinedMessage(slot int, id string) bool {
	_, oldRecord := s.messages[0][id]
	_, newRecord := s.messages[1][id]
	oldObserved := s.hasObservedMessageLocked(0, id)
	newObserved := s.hasObservedMessageLocked(1, id)
	if slot == 0 {
		return newRecord || !oldRecord && newObserved
	}
	return oldRecord && !newRecord && oldObserved
}

func (s *Store) Rows(ctx context.Context, f Filter) ([]Row, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if f.DateBefore < 0 || f.DateAfter < 0 || f.DateBefore > 3650 || f.DateAfter > 3650 {
		return nil, fmt.Errorf("date margin must be between 0 and 3650 days")
	}
	var windows [][2]string
	for _, date := range f.Dates {
		t, err := time.Parse(time.DateOnly, date)
		if err != nil {
			return nil, err
		}
		windows = append(windows, [2]string{t.AddDate(0, 0, -f.DateBefore).Format(time.DateOnly), t.AddDate(0, 0, f.DateAfter).Format(time.DateOnly)})
	}
	incidentSeconds := make(map[int64]bool, len(f.IncidentSeconds))
	incidentDays := make(map[string]bool, len(f.IncidentSeconds))
	for second := range f.IncidentSeconds {
		if second <= 0 {
			return nil, fmt.Errorf("invalid incident time")
		}
		incidentSeconds[second] = true
		incidentDays[time.Unix(second, 0).In(time.Local).Format(time.DateOnly)] = true
	}
	s.mu.RLock()
	a, b := s.snapshots[0], s.snapshots[1]
	ok, why := compatible(a, b)
	same := sameOwner(a, b)
	labels := serverLabels(s.effectiveChannelsLocked(same))
	lookup := s.nameLookupLocked()
	mode := f.Mode
	incidentAuto := (mode == "" || mode == "auto") && len(incidentSeconds) > 0 && ok
	if mode == "" || mode == "auto" {
		mode = "all"
		if ok && !incidentAuto {
			mode = "missing"
		}
	}
	if slices.Contains([]string{"missing", "present", "new"}, mode) && !ok {
		s.mu.RUnlock()
		return nil, fmt.Errorf("%s", cmp.Or(why, "Open both packages to compare"))
	}
	if !slices.Contains([]string{"missing", "present", "new", "older", "newer", "all"}, mode) {
		s.mu.RUnlock()
		return nil, fmt.Errorf("unknown view %q", mode)
	}
	if !slices.Contains([]string{"", "attachments", "media"}, f.Media) {
		s.mu.RUnlock()
		return nil, fmt.Errorf("unknown media filter %q", f.Media)
	}
	type item struct {
		m             Message
		sent          SentMessage
		c             channel
		name          string
		messageRecord bool
		foundInNew    bool
		missing       bool
	}
	var items []item
	for slot := range 2 {
		labelled := map[string]string{}
		if mode == "older" && slot != 0 || mode == "newer" && slot != 1 || mode == "new" && slot != 1 || (mode == "missing" || mode == "present") && slot != 0 {
			continue
		}
		if mode == "all" && !same && ((a != nil && slot != 0) || (a == nil && slot != 1)) {
			continue
		}
		candidates := s.observedMessagesLocked(slot)
		if len(windows) > 0 || f.From != "" || f.Until != "" || len(incidentSeconds) > 0 {
			candidates = func(yield func(observedMessage) bool) {
				for day, ids := range s.byDate[slot] {
					if ctx.Err() != nil {
						return
					}
					if f.From != "" && day < f.From || f.Until != "" && day >= f.Until {
						continue
					}
					if len(incidentDays) > 0 && !incidentDays[day] {
						continue
					}
					if len(windows) > 0 && !slices.ContainsFunc(windows, func(w [2]string) bool { return day >= w[0] && day <= w[1] }) {
						continue
					}
					for _, id := range ids {
						message, ok := s.observedMessageLocked(slot, id)
						if ok && !yield(message) {
							return
						}
					}
				}
			}
		}
		for observed := range candidates {
			if len(items)%1024 == 0 {
				if e := ctx.Err(); e != nil {
					s.mu.RUnlock()
					return nil, e
				}
			}
			m := observed.Message
			if len(incidentSeconds) > 0 {
				created, _ := time.Parse(time.RFC3339Nano, m.Date)
				messageMatch := !created.IsZero() && incidentSeconds[created.Unix()]
				eventMatch := !observed.Sent.Time.IsZero() && incidentSeconds[observed.Sent.Time.Unix()]
				if !messageMatch && !eventMatch {
					continue
				}
			}
			_, inOldRecord := s.messages[0][m.ID]
			_, inNewRecord := s.messages[1][m.ID]
			inOld := s.hasObservedMessageLocked(0, m.ID)
			if mode == "missing" && inNewRecord || mode == "present" && (!inOldRecord || !inNewRecord) || mode == "new" && inOld || mode == "all" && same && s.skipCombinedMessage(slot, m.ID) {
				continue
			}
			sent := observed.Sent
			if same {
				sent = mergeSentMessage(sent, s.sent[1-slot][m.ID])
			}
			if m.Channel == "" {
				m.Channel = sent.Channel
			}
			c := s.channels[slot][m.Channel]
			if same {
				c = merge(s.channels[0][m.Channel], s.channels[1][m.Channel])
			}
			c = merge(c, channelObservation("", sent.Guild, "", "guild", "", "", 2))
			c = applyServerLabel(c, labels)
			name, cached := labelled[m.Channel]
			if !cached {
				name = channelLabel(c, m.Channel, lookup)
				labelled[m.Channel] = name
			}
			items = append(items, item{m: m, sent: sent, c: c, name: name, messageRecord: observed.MessageRecord, foundInNew: inNewRecord, missing: same && slot == 0 && !inNewRecord})
		}
	}
	s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rows []Row
	search := strings.ToLower(f.Search)
	for i, v := range items {
		if i%1024 == 0 {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
		}
		m := v.m
		c := v.c
		if c.conflict {
			c.guild = ""
			c.kind = "conflict"
		}
		guildIncluded := len(f.Guilds) == 0 || slices.Contains(f.Guilds, c.guild)
		if !guildIncluded || slices.Contains(f.ExcludedGuilds, c.guild) || f.Channel != "" && f.Channel != m.Channel || f.Kind != "" && f.Kind != c.kind || f.Media == "attachments" && !m.HasAttachments || f.Media == "media" && !m.HasMedia || search != "" && !strings.Contains(strings.ToLower(m.Content), search) {
			continue
		}
		server := c.server
		if server == "" {
			switch c.kind {
			case "dm":
				server = "Direct messages"
			case "unknown-dm":
				server = "Unknown participants"
			case "group":
				server = "Group messages"
			default:
				server = "Unknown server"
			}
		}
		status := "observed"
		if v.missing {
			status = "missing"
		} else if incidentAuto && v.foundInNew {
			status = "found in newer"
		} else if mode == "present" || mode == "new" {
			status = mode
		} else if !v.messageRecord {
			status = "send event only"
		}
		sendTime := ""
		if !v.sent.Time.IsZero() {
			sendTime = v.sent.Time.Format(time.RFC3339Nano)
		}
		rows = append(rows, Row{ID: m.ID, Channel: m.Channel, Date: m.Date, Content: m.Content, Guild: c.guild, Server: server, Name: v.name, Kind: cmp.Or(c.kind, "unknown"), Status: status, HasAttachments: m.HasAttachments, HasMedia: m.HasMedia, AttachmentURLs: slices.Clone(m.AttachmentURLs), MessageRecord: v.messageRecord, SendEvent: v.sent.ID != "", Sources: sourceNames(v.sent.Sources, v.messageRecord), SendEventID: v.sent.EventID, SendTime: sendTime, Platform: v.sent.Platform, ReportedLength: v.sent.Length, ReportedWords: v.sent.Words, ReportedURLs: v.sent.URLs, ReportedFiles: v.sent.Attachments})
	}
	slices.SortFunc(rows, func(a, b Row) int {
		return cmp.Or(strings.Compare(a.Date, b.Date), cmp.Compare(len(a.ID), len(b.ID)), strings.Compare(a.ID, b.ID))
	})
	if f.Limit > 0 {
		start := min(max(0, f.Offset), len(rows))
		rows = rows[start:min(len(rows), start+f.Limit)]
	}
	return rows, nil
}
func (s *Store) Each(ctx context.Context, f Filter, fn func(Row) error) error {
	rows, e := s.Rows(ctx, f)
	if e != nil {
		return e
	}
	for _, r := range rows {
		if e = ctx.Err(); e != nil {
			return e
		}
		if e = fn(r); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) Groups(ctx context.Context, f Filter, dates bool) ([]Group, error) {
	f.Limit = 0
	f.Offset = 0
	rows, e := s.Rows(ctx, f)
	if e != nil {
		return nil, e
	}
	return GroupRows(rows, dates), nil
}
func GroupRows(rows []Row, dates bool) []Group {
	counts := map[[2]string]int{}
	for _, r := range rows {
		key := [2]string{r.Guild, r.Server}
		if dates {
			day := LocalDate(r.Date)
			key = [2]string{day, day}
		}
		counts[key]++
	}
	out := make([]Group, 0, len(counts))
	for k, n := range counts {
		out = append(out, Group{ID: k[0], Name: k[1], Count: n})
	}
	slices.SortFunc(out, func(a, b Group) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(a.ID, b.ID), strings.Compare(a.Name, b.Name))
	})
	return out
}
func (s *Store) Servers(ctx context.Context) ([]Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	channels := s.effectiveChannelsLocked(sameOwner(s.snapshots[0], s.snapshots[1]))
	labels := serverLabels(channels)
	groups := map[string]Group{}
	for _, c := range channels {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if c.guild != "" && !c.conflict {
			groups[c.guild] = Group{ID: c.guild, Name: cmp.Or(labels[c.guild].name, c.guild)}
		}
	}
	out := slices.Collect(maps.Values(groups))
	slices.SortFunc(out, func(a, b Group) int { return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID)) })
	return out, nil
}
func Day(id string) string {
	n, _ := strconv.ParseUint(id, 10, 64)
	return time.UnixMilli(int64(n>>22) + 1420070400000).UTC().Format("2006-01-02T15:04:05.000Z")
}
func LocalDate(date string) string {
	t, e := time.Parse(time.RFC3339Nano, date)
	if e != nil {
		return date
	}
	return t.In(time.Local).Format(time.DateOnly)
}
