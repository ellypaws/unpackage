package store

import (
	"cmp"
	"context"
	"encoding/json"
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
	mu              sync.RWMutex
	snapshots       [2]*Snapshot
	messages        [2]map[string]Message
	channels        [2]map[string]channel
	byDate          [2]map[string][]string
	byChannel       [2]map[string][]string
	recent          [2]map[string]string
	counts          [2]int
	changed         chan struct{}
	events          [2]map[string]Event
	sent            [2]map[string]SentMessage
	names           [2]map[string]identity
	serverNames     [2]map[string]serverLabel
	interned        map[string]string
	rowCacheMu      sync.Mutex
	rowCache        map[string]rowCacheEntry
	rowCacheBytes   int64
	rowCacheVersion uint64
}

const (
	rowCacheLimit = 256 << 20
	rowCacheTTL   = 5 * time.Minute
)

type rowCacheEntry struct {
	rows                []Row
	size                int64
	expiresAt, accessed time.Time
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
	Date                        string
	Kind                        string
	KindRank                    int
	Category                    string
	CategoryRank                int
	Time                        time.Time
	Platform                    string
	Length, Words               int
	URLs, Attachments           int
	HasMedia                    bool
	Sources                     ActivitySource
}
type Message struct {
	ID, Channel, Date, Content string
	Features                   []string
	AttachmentURLs             []string
	HasAttachments, HasMedia   bool
	HasLink                    bool
}
type ChannelObservation struct {
	ID, Name, Guild, Server, Kind, Category string
	Title, Recipients                       string
	Rank, GuildRank                         int
	ServerRank, KindRank, CategoryRank      int
}
type IdentityObservation struct {
	Username, GlobalName, Aliases string
	Rank                          int
}
type ServerObservation struct {
	ID, Name string
	Rank     int
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
	Category       string   `json:"category"`
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
	NameFallback   bool     `json:"channel_fallback,omitempty"`
	ServerFallback bool     `json:"server_fallback,omitempty"`
}

func (r Row) ContentUnavailable() bool {
	return r.Status == "missing" || !r.MessageRecord
}

type Group struct {
	ID, Name string
	Count    int
	Missing  int
	Fallback bool
}
type Filter struct {
	Mode, Search, Channel, Kind, Media string
	Guilds, ExcludedGuilds, Dates      []string
	ChannelTypes, ExcludedChannelTypes []string
	IncidentSeconds                    map[int64]int64
	From, Until                        string
	Limit, Offset                      int
	DateBefore, DateAfter              int
	HideEventOnly                      bool
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
	category                  string
	title, recipients         string
	nameRank, guildRank       int
	serverRank, kindRank      int
	categoryRank, titleRank   int
	conflict                  bool
	conflictRank              int
	privateRank               int
	dmRank, groupRank         int
	guildKindRank             int
}

type identity struct {
	username, globalName         string
	usernameRank, globalNameRank int
	aliases                      string
}

type serverLabel struct {
	name  string
	rank  int
	count int
}

type displayValue struct {
	text     string
	fallback bool
}

type serverIdentity struct {
	id, name string
}

type serverNameIndex struct {
	byName map[string]serverIdentity
	names  []serverIdentity
}

func New() *Store { return &Store{} }

func (s *Store) invalidateRows() {
	s.rowCacheMu.Lock()
	s.rowCacheVersion++
	s.rowCache = nil
	s.rowCacheBytes = 0
	s.rowCacheMu.Unlock()
}

func (s *Store) Clear(slot int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[slot] = nil
	s.messages[slot] = nil
	s.channels[slot] = nil
	s.byDate[slot] = nil
	s.byChannel[slot] = nil
	s.recent[slot] = nil
	s.counts[slot] = 0
	s.notifyLocked()
	s.events[slot] = nil
	s.sent[slot] = nil
	s.names[slot] = nil
	s.serverNames[slot] = nil
	s.invalidateRows()
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
	s.byChannel[slot] = map[string][]string{}
	s.recent[slot] = map[string]string{}
	s.counts[slot] = 0
	s.notifyLocked()
	s.events[slot] = map[string]Event{}
	s.sent[slot] = map[string]SentMessage{}
	s.names[slot] = map[string]identity{}
	s.serverNames[slot] = map[string]serverLabel{}
	s.invalidateRows()
	return nil
}

// Names records user identities keyed by user ID so conversations can display their participants.
func (s *Store) Names(ctx context.Context, slot int, names map[string]IdentityObservation) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.names[slot] == nil {
		return fmt.Errorf("slot is not open")
	}
	for id, observation := range names {
		if len(observation.Username) > 256 || len(observation.GlobalName) > 256 || len(observation.Aliases) > 4096 || (s.names[slot][id] == identity{} && len(s.names[slot]) >= 1<<16) {
			continue
		}
		s.names[slot][id] = mergeIdentity(s.names[slot][id], identityObservation(observation))
	}
	s.invalidateRows()
	return nil
}

// ServerNames records server display names independently of channel records.
func (s *Store) ServerNames(ctx context.Context, slot int, observations []ServerObservation) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.serverNames[slot] == nil {
		return fmt.Errorf("slot is not open")
	}
	for _, observation := range observations {
		if !digits(observation.ID) || observation.Name == "" || len(observation.Name) > 4096 {
			continue
		}
		candidate := serverLabel{name: observation.Name, rank: observation.Rank, count: 1}
		current := s.serverNames[slot][observation.ID]
		if current.name == candidate.name {
			current.rank = max(current.rank, candidate.rank)
			current.count++
			s.serverNames[slot][observation.ID] = current
		} else if candidate.rank > current.rank || candidate.rank == current.rank && strings.Compare(candidate.name, current.name) < 0 {
			s.serverNames[slot][observation.ID] = candidate
		}
	}
	s.invalidateRows()
	return nil
}

func identityObservation(observation IdentityObservation) identity {
	value := identity{username: observation.Username, globalName: observation.GlobalName, aliases: observation.Aliases}
	if value.username != "" {
		value.usernameRank = observation.Rank
		value.aliases = mergeRecipientEvidence(value.aliases, value.username)
	}
	if value.globalName != "" {
		value.globalNameRank = observation.Rank
		value.aliases = mergeRecipientEvidence(value.aliases, value.globalName)
	}
	return value
}

func mergeIdentity(a, b identity) identity {
	if preferField(a.username, a.usernameRank, b.username, b.usernameRank) {
		a.username = b.username
		a.usernameRank = b.usernameRank
	}
	if preferField(a.globalName, a.globalNameRank, b.globalName, b.globalNameRank) {
		a.globalName = b.globalName
		a.globalNameRank = b.globalNameRank
	}
	a.aliases = mergeRecipientEvidence(a.aliases, b.aliases)
	return a
}

func identityDisplay(value identity) string {
	switch {
	case value.username != "" && value.globalName != "" && !strings.EqualFold(value.username, value.globalName):
		return "@" + strings.TrimPrefix(value.username, "@") + " (" + value.globalName + ")"
	case value.username != "":
		return "@" + strings.TrimPrefix(value.username, "@")
	default:
		return value.globalName
	}
}

func (s *Store) identityLookupLocked() func(string) identity {
	return func(id string) identity {
		value := s.names[0][id]
		newer := s.names[1][id]
		if newer.username != "" {
			value.username = newer.username
			value.usernameRank = newer.usernameRank
		}
		if newer.globalName != "" {
			value.globalName = newer.globalName
			value.globalNameRank = newer.globalNameRank
		}
		value.aliases = mergeRecipientEvidence(value.aliases, newer.aliases)
		return value
	}
}

func (s *Store) nameLookupLocked() func(string) string {
	identityLookup := s.identityLookupLocked()
	return func(id string) string {
		return identityDisplay(identityLookup(id))
	}
}

// channelLabel names a channel for display using identities resolved from recipient IDs.
func channelLabel(c channel, id, owner string, lookup func(string) string) string {
	if c.kind == "dm" || c.kind == "unknown-dm" {
		for recipient := range strings.SplitSeq(c.recipients, "\n") {
			if recipient == owner {
				continue
			}
			name := ""
			if digits(recipient) {
				name = lookup(recipient)
			}
			if !unresolvedParticipant(name) {
				return directLabel(name)
			}
		}
		if !unresolvedParticipant(c.name) {
			return directLabel(c.name)
		}
		for recipient := range strings.SplitSeq(c.recipients, "\n") {
			if recipient == owner {
				continue
			}
			if !digits(recipient) && !unresolvedParticipant(recipient) {
				return directLabel(recipient)
			}
		}
		return "Unknown participant"
	}
	if c.kind != "group" {
		return cmp.Or(c.name, id)
	}
	var names []string
	otherRecipients := 0
	ownerPresent := false
	for recipient := range strings.SplitSeq(c.recipients, "\n") {
		if recipient == "" {
			continue
		}
		if recipient == owner {
			ownerPresent = true
			continue
		}
		otherRecipients++
		if name := lookup(recipient); name != "" {
			names = append(names, name)
		} else if !digits(recipient) && !unresolvedParticipant(recipient) {
			names = append(names, directLabel(recipient))
		}
	}
	participants := strings.Join(names, ", ")
	title := c.title
	if title == "" && !unresolvedChannelLabel(c.name) {
		if _, direct := ConversationParticipant(c.name); !direct {
			title = c.name
		}
	}
	switch {
	case title != "" && participants != "":
		return title + " (" + participants + ")"
	case title != "":
		return title
	case participants != "":
		return participants
	case ownerPresent && otherRecipients == 0:
		return "Group DM (only you remain)"
	case otherRecipients == 1:
		return "Group DM with 1 unavailable participant"
	case otherRecipients > 1:
		return fmt.Sprintf("Group DM with %d unavailable participants", otherRecipients)
	}
	return "Unnamed group DM"
}

func channelDisplay(c channel, id, owner string, lookup func(string) string) displayValue {
	label := channelLabel(c, id, owner, lookup)
	fallback := label == "" || label == id || unresolvedChannelLabel(label) || strings.HasPrefix(label, "Group DM with ") || label == "Group DM (only you remain)" || label == "Unnamed group DM"
	lower := strings.ToLower(strings.TrimSpace(label))
	if label == "" || label == id || lower == "none" || lower == "null" {
		label = "Unnamed channel"
		if suffix := idSuffix(id); suffix != "" {
			label += " …" + suffix
		}
	}
	return displayValue{text: label, fallback: fallback}
}

func serverDisplay(c channel) displayValue {
	if c.conflict {
		return displayValue{text: "Conflicting channel metadata", fallback: true}
	}
	switch c.kind {
	case "dm", "unknown-dm":
		return displayValue{text: "Direct messages", fallback: true}
	case "group":
		return displayValue{text: "Group messages", fallback: true}
	}
	if c.server != "" && !unresolvedChannelLabel(c.server) {
		return displayValue{text: c.server}
	}
	label := "Unknown server"
	if suffix := idSuffix(c.guild); suffix != "" {
		label += " …" + suffix
	}
	return displayValue{text: label, fallback: true}
}

func idSuffix(id string) string {
	if len(id) <= 4 {
		return id
	}
	return id[len(id)-4:]
}

func directLabel(name string) string {
	name = strings.TrimSpace(name)
	if participant, ok := ConversationParticipant(name); ok {
		name = participant
	}
	if head, tail, ok := strings.Cut(name, "#"); ok && tail != "" && strings.Trim(tail, "0123456789") == "" {
		name = head
	}
	return "@" + strings.TrimPrefix(name, "@")
}

func unresolvedParticipant(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "" || strings.Contains(name, "unknown participant") || name == "deleted user" || strings.HasSuffix(name, "with deleted user")
}

// ConversationParticipant extracts the participant from Discord's direct-message labels.
func ConversationParticipant(value string) (string, bool) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	for _, prefix := range []string{"direct message with ", "direct messages with "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(value[len(prefix):]), true
		}
	}
	if participant, ok := strings.CutPrefix(value, "@"); ok && strings.TrimSpace(participant) != "" {
		return strings.TrimSpace(participant), true
	}
	return "", false
}

// ClassifyChannelLabel interprets labels shared by Messages/index.json and Activity records.
func ClassifyChannelLabel(value string) (string, string, string) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if participant, ok := ConversationParticipant(value); ok {
		if unresolvedParticipant(participant) {
			return value, "", "unknown-dm"
		}
		return value, "", "dm"
	}
	if i := strings.LastIndex(lower, " in "); i >= 0 {
		return value[:i], value[i+4:], "guild"
	}
	if strings.HasPrefix(lower, "unknown channel, ") {
		return "Unknown channel", value[len("Unknown channel, "):], "guild"
	}
	if strings.EqualFold(value, "None") || value == "" {
		return "", "", "unknown"
	}
	return value, "", "unknown"
}

func digits(v string) bool {
	if v == "" {
		return false
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || n == 0 {
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
	a.Date = cmp.Or(a.Date, b.Date)
	a.EventID = mergeText(a.EventID, b.EventID)
	a.Channel = mergeText(a.Channel, b.Channel)
	a.Guild = mergeText(a.Guild, b.Guild)
	if a.Kind == "dm" && b.Kind == "unknown-dm" || a.Kind == "unknown-dm" && b.Kind == "dm" {
		a.Kind = "dm"
		a.KindRank = max(a.KindRank, b.KindRank)
	} else if preferField(a.Kind, a.KindRank, b.Kind, b.KindRank) {
		a.Kind = b.Kind
		a.KindRank = b.KindRank
	}
	if preferField(a.Category, a.CategoryRank, b.Category, b.CategoryRank) {
		a.Category = b.Category
		a.CategoryRank = b.CategoryRank
	}
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

func sentChannel(sent SentMessage) channel {
	kind := sent.Kind
	rank := sent.KindRank
	if kind == "" && sent.Guild != "" {
		kind = "guild"
		rank = 2
	}
	return channelObservation("", sent.Guild, "", kind, sent.Category, "", "", rank, rank, sent.CategoryRank)
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
		if !alreadySent {
			message.Date = Day(message.ID)
		}
		s.sent[slot][message.ID] = mergeSentMessage(s.sent[slot][message.ID], message)
		if _, hasRecord := s.messages[slot][message.ID]; !hasRecord && !alreadySent {
			s.counts[slot]++
			day := LocalDate(message.Date)
			s.byDate[slot][day] = append(s.byDate[slot][day], message.ID)
			s.recent[slot][message.Channel] = max(s.recent[slot][message.Channel], message.Date)
		}
	}
	if snapshot := s.snapshots[slot]; snapshot != nil {
		snapshot.Count = int64(s.messageCountLocked(slot))
	}
	s.invalidateRows()
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
	previous := s.snapshots[slot]
	v.Slot = slot
	v.Count = int64(s.messageCountLocked(slot))
	s.snapshots[slot] = &v
	s.notifyLocked()
	if previous == nil || previous.Owner != v.Owner || previous.State != v.State || previous.Path != v.Path || previous.Complete != v.Complete {
		s.invalidateRows()
	}
}

func (s *Store) messageCountLocked(slot int) int {
	return s.counts[slot]
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
		s.byChannel[slot][m.Channel] = append(s.byChannel[slot][m.Channel], m.ID)
		s.recent[slot][m.Channel] = max(s.recent[slot][m.Channel], m.Date)
		if _, indexed := s.sent[slot][m.ID]; !indexed {
			s.counts[slot]++
			day := LocalDate(m.Date)
			s.byDate[slot][day] = append(s.byDate[slot][day], m.ID)
		}
	}
	s.snapshots[slot].Count = int64(s.messageCountLocked(slot))
	s.invalidateRows()
	return nil
}

func (s *Store) notifyLocked() {
	if s.changed != nil {
		close(s.changed)
	}
	s.changed = make(chan struct{})
}

func (s *Store) MessagesLoading() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.messagesLoadingLocked()
}

func (s *Store) messagesLoadingLocked() bool {
	for _, snapshot := range s.snapshots {
		if snapshot != nil && snapshot.State == "loading" && !snapshot.Complete && snapshot.Phase != "waiting for messages" && snapshot.Phase != "activity" {
			return true
		}
	}
	return false
}

func (s *Store) WaitMessages(ctx context.Context) error {
	for {
		s.mu.RLock()
		pending, changed := s.messagesLoadingLocked(), s.changed
		s.mu.RUnlock()
		if !pending {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}
func merge(a, b channel) channel {
	a = mergeGuildEvidence(a, b)
	a.privateRank = max(a.privateRank, b.privateRank)
	a.dmRank = max(a.dmRank, b.dmRank)
	a.groupRank = max(a.groupRank, b.groupRank)
	a.guildKindRank = max(a.guildKindRank, b.guildKindRank)
	if preferChannelName(a.name, a.nameRank, b.name, b.nameRank) {
		a.name = b.name
		a.nameRank = b.nameRank
	}
	if preferDisplayField(a.server, a.serverRank, b.server, b.serverRank) {
		a.server = b.server
		a.serverRank = b.serverRank
	}
	if preferDisplayField(a.title, a.titleRank, b.title, b.titleRank) {
		a.title = b.title
		a.titleRank = b.titleRank
	}
	if preferField(a.category, a.categoryRank, b.category, b.categoryRank) {
		a.category = b.category
		a.categoryRank = b.categoryRank
	}
	a.recipients = mergeRecipientEvidence(a.recipients, b.recipients)
	if a.kind == "dm" && b.kind == "unknown-dm" || a.kind == "unknown-dm" && b.kind == "dm" {
		a.kind = "dm"
		a.kindRank = max(a.kindRank, b.kindRank)
	} else if b.kind != "unknown" && preferField(a.kind, a.kindRank, b.kind, b.kindRank) {
		a.kind = b.kind
		a.kindRank = b.kindRank
	}
	return resolveChannelEvidence(a)
}

func mergeGuildEvidence(a, b channel) channel {
	if b.guild == "" {
		return a
	}
	if a.guild == "" || b.guildRank > a.guildRank {
		a.guild = b.guild
		a.guildRank = b.guildRank
		a.conflict = b.conflict
		a.conflictRank = b.conflictRank
		return a
	}
	if b.guildRank < a.guildRank {
		return a
	}
	if a.guild == b.guild {
		if a.conflictRank < a.guildRank {
			a.conflict = false
			a.conflictRank = 0
		}
		if b.conflict && b.conflictRank >= a.guildRank {
			a.conflict = true
			a.conflictRank = b.conflictRank
		}
		return a
	}
	if strings.Compare(b.guild, a.guild) < 0 {
		a.guild = b.guild
	}
	a.conflict = true
	a.conflictRank = a.guildRank
	return a
}

func resolveChannelEvidence(c channel) channel {
	privateKindRank := max(c.privateRank, c.dmRank, c.groupRank)
	if c.guildKindRank > privateKindRank {
		c.kind = "guild"
		c.kindRank = c.guildKindRank
		return c
	}
	if c.guildKindRank > 0 && c.guildKindRank == privateKindRank {
		c.kind = "conflict"
		c.kindRank = c.guildKindRank
		c.conflict = true
		c.conflictRank = c.guildKindRank
		return c
	}
	if privateKindRank > 0 {
		c.guild = ""
		c.server = ""
		c.conflict = false
		c.conflictRank = 0
	}
	if c.groupRank > c.dmRank {
		c.kind = "group"
		c.kindRank = c.groupRank
		return c
	}
	if c.dmRank > c.groupRank {
		c.kind = "dm"
		c.kindRank = c.dmRank
		return c
	}
	if c.dmRank > 0 {
		c.kind = "conflict"
		c.kindRank = c.dmRank
		c.conflict = true
		c.conflictRank = c.dmRank
		return c
	}
	if c.privateRank > 0 {
		c.kind = "unknown-dm"
		c.kindRank = c.privateRank
		return c
	}
	if c.kind == "unknown" {
		c.kind = ""
		c.kindRank = 0
	}
	if c.kind == "" && c.guild != "" {
		c.kind = "guild"
		c.kindRank = c.guildRank
	}
	return c
}

func mergeRecipientEvidence(a, b string) string {
	if b == "" || a == b {
		return a
	}
	if a == "" {
		return b
	}
	values := map[string]bool{}
	for value := range strings.SplitSeq(a+"\n"+b, "\n") {
		if value = strings.TrimSpace(value); value != "" {
			values[value] = true
		}
	}
	items := slices.Sorted(maps.Keys(values))
	var out strings.Builder
	for _, item := range items {
		added := len(item)
		if out.Len() > 0 {
			added++
		}
		if out.Len()+added > 4096 {
			break
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(item)
	}
	return out.String()
}

func preferChannelName(current string, currentRank int, candidate string, candidateRank int) bool {
	currentUnknown := unresolvedChannelLabel(current)
	candidateUnknown := unresolvedChannelLabel(candidate)
	if current != "" && candidate != "" && currentUnknown != candidateUnknown {
		return currentUnknown
	}
	return preferField(current, currentRank, candidate, candidateRank)
}

func preferDisplayField(current string, currentRank int, candidate string, candidateRank int) bool {
	currentUnknown := unresolvedChannelLabel(current)
	candidateUnknown := unresolvedChannelLabel(candidate)
	if current != "" && candidate != "" && currentUnknown != candidateUnknown {
		return currentUnknown
	}
	return preferField(current, currentRank, candidate, candidateRank)
}

func unresolvedChannelLabel(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return unresolvedParticipant(value) || value == "none" || value == "null" || strings.HasPrefix(value, "unknown channel") || strings.HasPrefix(value, "unnamed channel") || strings.HasPrefix(value, "unknown server")
}

func preferField(current string, currentRank int, candidate string, candidateRank int) bool {
	return candidate != "" && (current == "" || candidateRank > currentRank || candidateRank == currentRank && strings.Compare(candidate, current) < 0)
}

func channelObservation(name, guild, server, kind, category, title, recipients string, rank, kindRank, categoryRank int) channel {
	_, _, labelKind := ClassifyChannelLabel(name)
	labelPrivate := guild == "" && kind != "guild" && (labelKind == "dm" || labelKind == "unknown-dm")
	c := channel{name: name, guild: guild, server: server, kind: kind, category: category, title: title, recipients: recipients}
	if labelPrivate {
		c.privateRank = rank
	}
	if kind == "dm" || kind == "unknown-dm" || kind == "group" {
		c.privateRank = max(c.privateRank, kindRank)
	}
	if kind == "dm" || labelPrivate && labelKind == "dm" {
		c.dmRank = kindRank
	}
	if kind == "group" {
		c.groupRank = kindRank
	}
	if kind == "guild" {
		c.guildKindRank = kindRank
	}
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
		c.kindRank = kindRank
	}
	if category != "" {
		c.categoryRank = categoryRank
	}
	if title != "" {
		c.titleRank = rank
	}
	return c
}

func channelCategory(c channel) string {
	switch c.kind {
	case "dm", "unknown-dm":
		return "dm"
	case "group":
		return "group"
	case "guild":
		if c.category == "thread" {
			return "thread"
		}
		return "server"
	default:
		return "unknown"
	}
}

func channelTypeAllowed(included, excluded []string, category string) bool {
	return (len(included) == 0 || slices.Contains(included, category)) && !slices.Contains(excluded, category)
}

func sameOwner(a, b *Snapshot) bool {
	return a != nil && b != nil && a.Owner != "" && a.Owner == b.Owner
}

func (s *Store) effectiveChannelsLocked(combine bool) []channel {
	index := s.serverNameIndexLocked(combine)
	if !combine {
		slot := 1
		if s.snapshots[0] != nil {
			slot = 0
		}
		out := slices.Collect(maps.Values(s.channels[slot]))
		for i := range out {
			out[i] = resolveChannelServer(out[i], index)
		}
		return out
	}
	byID := make(map[string]channel, len(s.channels[0])+len(s.channels[1]))
	for _, channels := range s.channels {
		for id, c := range channels {
			byID[id] = merge(byID[id], c)
		}
	}
	out := slices.Collect(maps.Values(byID))
	for i := range out {
		out[i] = resolveChannelServer(out[i], index)
	}
	return out
}

func (s *Store) includeSlotLocked(combine bool, slot int) bool {
	return combine || s.snapshots[0] != nil && slot == 0 || s.snapshots[0] == nil && slot == 1
}

func (s *Store) serverNameIndexLocked(combine bool) serverNameIndex {
	index := serverNameIndex{byName: map[string]serverIdentity{}}
	add := func(id, name string) {
		name = strings.TrimSpace(name)
		if !digits(id) || name == "" || unresolvedChannelLabel(name) {
			return
		}
		key := strings.ToLower(name)
		current, exists := index.byName[key]
		if exists && current.id != id {
			index.byName[key] = serverIdentity{name: name}
			return
		}
		index.byName[key] = serverIdentity{id: id, name: name}
	}
	for slot := range 2 {
		if !s.includeSlotLocked(combine, slot) {
			continue
		}
		for id, label := range s.serverNames[slot] {
			add(id, label.name)
		}
		for _, c := range s.channels[slot] {
			add(c.guild, c.server)
		}
	}
	for _, identity := range index.byName {
		if identity.id != "" {
			index.names = append(index.names, identity)
		}
	}
	slices.SortFunc(index.names, func(a, b serverIdentity) int {
		return cmp.Or(cmp.Compare(len(b.name), len(a.name)), strings.Compare(a.name, b.name), strings.Compare(a.id, b.id))
	})
	return index
}

func resolveChannelServer(c channel, index serverNameIndex) channel {
	if c.guild != "" || c.kind == "dm" || c.kind == "unknown-dm" || c.kind == "group" || c.server == "" {
		return c
	}
	full := strings.TrimSpace(c.name + " in " + c.server)
	identity := index.byName[strings.ToLower(strings.TrimSpace(c.server))]
	if identity.id == "" {
		for _, candidate := range index.names {
			suffix := " in " + candidate.name
			if len(full) >= len(suffix) && strings.EqualFold(full[len(full)-len(suffix):], suffix) {
				identity = candidate
				c.name = strings.TrimSpace(full[:len(full)-len(suffix)])
				break
			}
		}
	}
	if identity.id != "" {
		c.guild = identity.id
		c.guildRank = max(c.guildRank, c.serverRank)
		c.server = identity.name
	}
	return c
}

func (s *Store) serverLabelsLocked(channels []channel, combine bool) map[string]serverLabel {
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
	for slot, names := range s.serverNames {
		if !s.includeSlotLocked(combine, slot) {
			continue
		}
		for guild, observed := range names {
			if observed.name == "" {
				continue
			}
			if candidates[guild] == nil {
				candidates[guild] = map[string]serverLabel{}
			}
			candidate := candidates[guild][observed.name]
			candidate.name = observed.name
			candidate.rank = max(candidate.rank, observed.rank)
			candidate.count += observed.count
			candidates[guild][observed.name] = candidate
		}
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
		kindRank := observation.Rank
		if observation.Kind != "" && observation.KindRank > 0 {
			kindRank = observation.KindRank
		}
		categoryRank := observation.Rank
		if observation.Category != "" && observation.CategoryRank > 0 {
			categoryRank = observation.CategoryRank
		}
		candidate := channelObservation(observation.Name, observation.Guild, observation.Server, observation.Kind, observation.Category, observation.Title, observation.Recipients, observation.Rank, kindRank, categoryRank)
		if observation.Guild != "" && observation.GuildRank > 0 {
			candidate.guildRank = observation.GuildRank
		}
		if observation.Server != "" && observation.ServerRank > 0 {
			candidate.serverRank = observation.ServerRank
		}
		s.channels[slot][observation.ID] = merge(s.channels[slot][observation.ID], candidate)
	}
	s.invalidateRows()
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
		m = Message{ID: id, Channel: sent.Channel, Date: sent.Date, HasAttachments: sent.Attachments > 0, HasMedia: sent.HasMedia, HasLink: sent.URLs > 0}
	} else {
		m.HasAttachments = m.HasAttachments || sent.Attachments > 0
		m.HasMedia = m.HasMedia || sent.HasMedia
		m.HasLink = m.HasLink || sent.URLs > 0
	}
	return observedMessage{Message: m, Sent: sent, MessageRecord: messageRecord}, true
}

func (s *Store) observedMessagesLocked(slot int) iter.Seq[observedMessage] {
	return func(yield func(observedMessage) bool) {
		for id, m := range s.messages[slot] {
			sent := s.sent[slot][id]
			m.HasAttachments = m.HasAttachments || sent.Attachments > 0
			m.HasMedia = m.HasMedia || sent.HasMedia
			m.HasLink = m.HasLink || sent.URLs > 0
			message := observedMessage{Message: m, Sent: sent, MessageRecord: true}
			if !yield(message) {
				return
			}
		}
		for id, sent := range s.sent[slot] {
			if _, ok := s.messages[slot][id]; ok {
				continue
			}
			message := observedMessage{ID: id, Channel: sent.Channel, Date: sent.Date, HasAttachments: sent.Attachments > 0, HasMedia: sent.HasMedia, HasLink: sent.URLs > 0, Sent: sent}
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

func rowFilterKey(f Filter) string {
	f.Limit = 0
	f.Offset = 0
	encoded, _ := json.Marshal(f)
	return string(encoded)
}

func (s *Store) cachedRows(key string, now time.Time) ([]Row, uint64, bool) {
	s.rowCacheMu.Lock()
	defer s.rowCacheMu.Unlock()
	version := s.rowCacheVersion
	entry, ok := s.rowCache[key]
	if !ok {
		return nil, version, false
	}
	if !now.Before(entry.expiresAt) {
		delete(s.rowCache, key)
		s.rowCacheBytes -= entry.size
		return nil, version, false
	}
	entry.accessed = now
	s.rowCache[key] = entry
	return entry.rows, version, true
}

func (s *Store) CachedRows(f Filter) ([]Row, bool) {
	offset, limit := f.Offset, f.Limit
	f.Offset, f.Limit = 0, 0
	rows, _, ok := s.cachedRows(rowFilterKey(f), time.Now())
	if !ok {
		return nil, false
	}
	return pageRows(rows, offset, limit), true
}

func cachedRowsSize(key string, rows []Row) int64 {
	size := int64(len(key))
	for _, row := range rows {
		size += 288 + int64(len(row.ID)+len(row.Channel)+len(row.Date)+len(row.Content)+len(row.Guild)+len(row.Server)+len(row.Name)+len(row.Kind)+len(row.Category)+len(row.Status)+len(row.SendEventID)+len(row.SendTime)+len(row.Platform))
		for _, value := range row.AttachmentURLs {
			size += 16 + int64(len(value))
		}
		for _, value := range row.Sources {
			size += 16 + int64(len(value))
		}
	}
	return size
}

func (s *Store) cacheRows(key string, version uint64, rows []Row, now time.Time) {
	size := cachedRowsSize(key, rows)
	if size > rowCacheLimit {
		return
	}
	s.rowCacheMu.Lock()
	defer s.rowCacheMu.Unlock()
	if version != s.rowCacheVersion {
		return
	}
	if s.rowCache == nil {
		s.rowCache = map[string]rowCacheEntry{}
	}
	if previous, ok := s.rowCache[key]; ok {
		s.rowCacheBytes -= previous.size
		delete(s.rowCache, key)
	}
	for cacheKey, entry := range s.rowCache {
		if !now.Before(entry.expiresAt) {
			s.rowCacheBytes -= entry.size
			delete(s.rowCache, cacheKey)
		}
	}
	for s.rowCacheBytes+size > rowCacheLimit {
		oldestKey := ""
		var oldest time.Time
		for cacheKey, entry := range s.rowCache {
			if oldestKey == "" || entry.accessed.Before(oldest) {
				oldestKey = cacheKey
				oldest = entry.accessed
			}
		}
		if oldestKey == "" {
			break
		}
		s.rowCacheBytes -= s.rowCache[oldestKey].size
		delete(s.rowCache, oldestKey)
	}
	s.rowCache[key] = rowCacheEntry{rows: rows, size: size, expiresAt: now.Add(rowCacheTTL), accessed: now}
	s.rowCacheBytes += size
}

func pageRows(rows []Row, offset, limit int) []Row {
	if limit <= 0 {
		return rows
	}
	start := min(max(0, offset), len(rows))
	return slices.Clone(rows[start:min(len(rows), start+limit)])
}

func (s *Store) Rows(ctx context.Context, f Filter) ([]Row, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	offset, limit := f.Offset, f.Limit
	f.Offset, f.Limit = 0, 0
	cacheKey := rowFilterKey(f)
	now := time.Now()
	cached, cacheVersion, ok := s.cachedRows(cacheKey, now)
	if ok {
		return pageRows(cached, offset, limit), nil
	}
	query, err := ParseSearch(f.Search)
	if err != nil {
		return nil, err
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
	labels := s.serverLabelsLocked(s.effectiveChannelsLocked(same), same)
	serverIndex := s.serverNameIndexLocked(same)
	lookup := s.nameLookupLocked()
	for i, token := range query.Tokens {
		if token.Key == "mentions" && !digits(token.Value) {
			for _, names := range s.names {
				for id, person := range names {
					if identitySearchEqual(token.Value, person) {
						query.Tokens[i].Value = id
					}
				}
			}
		}
	}
	mode := f.Mode
	incidentAuto := (mode == "" || mode == "auto") && len(incidentSeconds) > 0 && ok
	if mode == "" || mode == "auto" {
		mode = "all"
		if ok && !incidentAuto {
			mode = "missing"
		}
	}
	if slices.Contains([]string{"present", "new"}, mode) && !ok {
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
	for _, category := range append(slices.Clone(f.ChannelTypes), f.ExcludedChannelTypes...) {
		if !slices.Contains([]string{"server", "dm", "group", "thread", "unknown"}, category) {
			s.mu.RUnlock()
			return nil, fmt.Errorf("unknown channel type %q", category)
		}
	}
	type resolvedChannel struct {
		channel channel
		name    displayValue
		allowed bool
	}
	type channelKey struct{ id, guild, kind, category string }
	type item struct {
		m             Message
		sent          SentMessage
		c             *resolvedChannel
		messageRecord bool
		foundInNew    bool
		missing       bool
	}
	capacity := s.counts[0] + s.counts[1]
	if f.Channel != "" {
		capacity = len(s.byChannel[0][f.Channel]) + len(s.byChannel[1][f.Channel])
	}
	if len(f.Dates) > 0 || len(f.IncidentSeconds) > 0 || f.From != "" || f.Until != "" || len(f.Guilds) > 0 || slices.ContainsFunc(query.Tokens, func(t SearchToken) bool {
		return slices.Contains([]string{"server", "in", "from", "type", "id"}, t.Key)
	}) {
		capacity = min(capacity, 4096)
	}
	items := make([]item, 0, capacity)
	for slot := range 2 {
		resolvedChannels := map[channelKey]*resolvedChannel{}
		if mode == "older" && slot != 0 || mode == "newer" && slot != 1 || mode == "new" && slot != 1 || mode == "present" && slot != 0 {
			continue
		}
		if (mode == "all" || mode == "missing") && !same && ((a != nil && slot != 0) || (a == nil && slot != 1)) {
			continue
		}
		candidates := s.observedMessagesLocked(slot)
		var ids []string
		for _, token := range query.Tokens {
			if token.Key == "id" && !token.Exclude {
				ids = append(ids, token.Value)
			}
		}
		if len(ids) > 0 {
			candidates = func(yield func(observedMessage) bool) {
				seen := map[string]bool{}
				for _, id := range ids {
					if seen[id] {
						continue
					}
					seen[id] = true
					observed, ok := s.observedMessageLocked(slot, id)
					if ok && !yield(observed) {
						return
					}
				}
			}
		}
		if f.Channel != "" {
			candidates = func(yield func(observedMessage) bool) {
				for _, id := range s.byChannel[slot][f.Channel] {
					observed, ok := s.observedMessageLocked(slot, id)
					if ok && !yield(observed) {
						return
					}
				}
				for id, sent := range s.sent[slot] {
					if sent.Channel != f.Channel {
						continue
					}
					if _, found := s.messages[slot][id]; found {
						continue
					}
					observed, _ := s.observedMessageLocked(slot, id)
					if !yield(observed) {
						return
					}
				}
			}
		}
		hasSearchDates := slices.ContainsFunc(query.Tokens, func(t SearchToken) bool { return slices.Contains([]string{"before", "after", "on"}, t.Key) })
		if len(windows) > 0 || f.From != "" || f.Until != "" || len(incidentSeconds) > 0 || hasSearchDates {
			candidates = func(yield func(observedMessage) bool) {
				for day, ids := range s.byDate[slot] {
					if ctx.Err() != nil {
						return
					}
					if hasSearchDates && !query.matches([]string{"before", "after", "on"}, func(_ int, t SearchToken) bool {
						switch t.Key {
						case "before":
							return day < t.Value
						case "after":
							return day > t.Value
						default:
							return day == t.Value
						}
					}) {
						continue
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
		visited := 0
		for observed := range candidates {
			visited++
			if visited%1024 == 0 {
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
			missing := !observed.MessageRecord
			if same {
				missing = !inNewRecord
			}
			skipCombined := same && s.skipCombinedMessage(slot, m.ID)
			skip := mode == "missing" && (!missing || skipCombined) ||
				mode == "present" && (!inOldRecord || !inNewRecord) ||
				mode == "new" && inOld ||
				mode == "all" && skipCombined
			if skip {
				continue
			}
			if f.HideEventOnly && !observed.MessageRecord && !(same && (inOldRecord || inNewRecord)) {
				continue
			}
			sent := observed.Sent
			if same {
				sent = mergeSentMessage(sent, s.sent[1-slot][m.ID])
			}
			m.HasLink = m.HasLink || sent.URLs > 0
			if m.Channel == "" {
				m.Channel = sent.Channel
			}
			if f.Channel != "" && f.Channel != m.Channel {
				continue
			}
			key := channelKey{m.Channel, sent.Guild, sent.Kind, sent.Category}
			resolved, cached := resolvedChannels[key]
			if !cached {
				c := s.channels[slot][m.Channel]
				if same {
					c = merge(s.channels[0][m.Channel], s.channels[1][m.Channel])
				}
				c = resolveChannelServer(c, serverIndex)
				c = applyServerLabel(merge(c, sentChannel(sent)), labels)
				owner := ""
				if s.snapshots[slot] != nil {
					owner = s.snapshots[slot].Owner
				}
				name := channelDisplay(c, m.Channel, owner, lookup)
				guild := c.guild
				if c.conflict {
					guild = ""
				}
				category := channelCategory(c)
				allowed := (len(f.Guilds) == 0 || slices.Contains(f.Guilds, guild)) && !slices.Contains(f.ExcludedGuilds, guild) && (f.Kind == "" || f.Kind == c.kind) && channelTypeAllowed(f.ChannelTypes, f.ExcludedChannelTypes, category) && query.channelMatches(c, m.Channel, name.text, owner, lookup)
				resolved = &resolvedChannel{c, name, allowed}
				resolvedChannels[key] = resolved
			}
			if !resolved.allowed || f.Media == "attachments" && !m.HasAttachments || f.Media == "media" && !m.HasMedia {
				continue
			}
			items = append(items, item{m: m, sent: sent, c: resolved, messageRecord: observed.MessageRecord, foundInNew: inNewRecord, missing: missing})
		}
	}
	s.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	order := make([]int, 0, len(items))
	for i, v := range items {
		if i%1024 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if query.messageMatches(v.m) {
			order = append(order, i)
		}
	}
	slices.SortFunc(order, func(i, j int) int {
		a, b := items[i].m, items[j].m
		return cmp.Or(strings.Compare(a.Date, b.Date), cmp.Compare(len(a.ID), len(b.ID)), strings.Compare(a.ID, b.ID))
	})
	rows := make([]Row, 0, len(order))
	for i, index := range order {
		v := items[index]
		if i%1024 == 0 {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
		}
		m := v.m
		c := v.c.channel
		if c.conflict {
			c.guild = ""
			c.kind = "conflict"
		}
		server := serverDisplay(c)
		status := "observed"
		if !v.messageRecord {
			status = "send event only"
		} else if v.missing {
			status = "missing"
		} else if incidentAuto && v.foundInNew {
			status = "found in newer"
		} else if mode == "present" || mode == "new" {
			status = mode
		}
		sendTime := ""
		if !v.sent.Time.IsZero() {
			sendTime = v.sent.Time.Format(time.RFC3339Nano)
		}
		rows = append(rows, Row{ID: m.ID, Channel: m.Channel, Date: m.Date, Content: m.Content, Guild: c.guild, Server: server.text, Name: v.c.name.text, Kind: cmp.Or(c.kind, "unknown"), Category: channelCategory(c), Status: status, HasAttachments: m.HasAttachments, HasMedia: m.HasMedia, AttachmentURLs: slices.Clone(m.AttachmentURLs), MessageRecord: v.messageRecord, SendEvent: v.sent.ID != "", Sources: sourceNames(v.sent.Sources, v.messageRecord), SendEventID: v.sent.EventID, SendTime: sendTime, Platform: v.sent.Platform, ReportedLength: v.sent.Length, ReportedWords: v.sent.Words, ReportedURLs: v.sent.URLs, ReportedFiles: v.sent.Attachments, NameFallback: v.c.name.fallback, ServerFallback: server.fallback})
	}
	s.cacheRows(cacheKey, cacheVersion, rows, now)
	return pageRows(rows, offset, limit), nil
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
	groups := map[[2]string]Group{}
	for _, r := range rows {
		key := [2]string{r.Guild, r.Server}
		fallback := r.ServerFallback
		if dates {
			day := LocalDate(r.Date)
			key = [2]string{day, day}
			fallback = false
		}
		group := groups[key]
		group.ID = key[0]
		group.Name = key[1]
		if group.Count == 0 {
			group.Fallback = fallback
		} else {
			group.Fallback = group.Fallback && fallback
		}
		group.Count++
		if r.ContentUnavailable() {
			group.Missing++
		}
		groups[key] = group
	}
	out := slices.Collect(maps.Values(groups))
	slices.SortFunc(out, func(a, b Group) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(a.ID, b.ID), strings.Compare(a.Name, b.Name))
	})
	return out
}
func (s *Store) Servers(ctx context.Context) ([]Group, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	combine := sameOwner(s.snapshots[0], s.snapshots[1])
	allChannels := s.effectiveChannelsLocked(combine)
	labels := s.serverLabelsLocked(allChannels, combine)
	index := s.serverNameIndexLocked(combine)
	active := map[string]bool{}
	for slot := range 2 {
		if !s.includeSlotLocked(combine, slot) {
			continue
		}
		for id := range s.recent[slot] {
			active[id] = true
		}
	}
	channels := make([]channel, 0, len(active))
	for id := range active {
		var c channel
		for slot := range 2 {
			if s.includeSlotLocked(combine, slot) {
				c = merge(c, s.channels[slot][id])
			}
		}
		channels = append(channels, resolveChannelServer(c, index))
	}
	groups := map[string]Group{}
	for _, c := range channels {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if c.guild != "" && !c.conflict {
			display := serverDisplay(applyServerLabel(c, labels))
			groups[c.guild] = Group{ID: c.guild, Name: display.text, Fallback: display.fallback}
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
