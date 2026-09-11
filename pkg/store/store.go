package store

import (
	"cmp"
	"context"
	"fmt"
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
}
type Message struct {
	ID, Channel, Date, Content string
	HasAttachments, HasMedia   bool
}
type ChannelObservation struct {
	ID, Name, Guild, Server, Kind string
	Rank                          int
}
type Row struct {
	ID             string `json:"message_id"`
	Channel        string `json:"channel_id"`
	Date           string `json:"date"`
	Content        string `json:"content"`
	Guild          string `json:"server_id"`
	Server         string `json:"server"`
	Name           string `json:"channel"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	HasAttachments bool   `json:"has_attachments"`
	HasMedia       bool   `json:"has_media"`
}
type Group struct {
	ID, Name string
	Count    int
}
type Filter struct {
	Mode, Search, Channel, Kind, Media string
	Guilds, Dates                      []string
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
	return nil
}
func (s *Store) SetSnapshot(slot int, v Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v.Slot = slot
	v.Count = int64(len(s.messages[slot]))
	s.snapshots[slot] = &v
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
		day := LocalDate(m.Date)
		s.byDate[slot][day] = append(s.byDate[slot][day], m.ID)
	}
	s.snapshots[slot].Count = int64(len(s.messages[slot]))
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

func channelObservation(name, guild, server, kind string, rank int) channel {
	c := channel{name: name, guild: guild, server: server, kind: kind}
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
	return s.Channels(ctx, slot, []ChannelObservation{{id, name, guild, server, kind, rank}})
}
func (s *Store) Channels(ctx context.Context, slot int, observations []ChannelObservation) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	for _, observation := range observations {
		if len(observation.Name) > 4096 || len(observation.Server) > 4096 {
			return fmt.Errorf("channel name too long")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, observation := range observations {
		if observation.ID == "" {
			continue
		}
		s.channels[slot][observation.ID] = merge(s.channels[slot][observation.ID], channelObservation(observation.Name, observation.Guild, observation.Server, observation.Kind, observation.Rank))
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
	s.mu.RLock()
	a, b := s.snapshots[0], s.snapshots[1]
	ok, why := compatible(a, b)
	same := sameOwner(a, b)
	labels := serverLabels(s.effectiveChannelsLocked(same))
	mode := f.Mode
	if mode == "" || mode == "auto" {
		mode = "all"
		if ok {
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
		m Message
		c channel
	}
	var items []item
	for slot, ms := range s.messages {
		if mode == "older" && slot != 0 || mode == "newer" && slot != 1 || mode == "new" && slot != 1 || (mode == "missing" || mode == "present") && slot != 0 {
			continue
		}
		if mode == "all" && !same && ((a != nil && slot != 0) || (a == nil && slot != 1)) {
			continue
		}
		candidates := maps.Values(ms)
		if len(windows) > 0 || f.From != "" || f.Until != "" {
			candidates = func(yield func(Message) bool) {
				for day, ids := range s.byDate[slot] {
					if ctx.Err() != nil {
						return
					}
					if f.From != "" && day < f.From || f.Until != "" && day >= f.Until {
						continue
					}
					if len(windows) > 0 && !slices.ContainsFunc(windows, func(w [2]string) bool { return day >= w[0] && day <= w[1] }) {
						continue
					}
					for _, id := range ids {
						if !yield(ms[id]) {
							return
						}
					}
				}
			}
		}
		for m := range candidates {
			if len(items)%1024 == 0 {
				if e := ctx.Err(); e != nil {
					s.mu.RUnlock()
					return nil, e
				}
			}
			_, inOld := s.messages[0][m.ID]
			_, inNew := s.messages[1][m.ID]
			if mode == "missing" && inNew || mode == "present" && !inNew || mode == "new" && inOld || mode == "all" && same && slot == 0 && inNew {
				continue
			}
			c := s.channels[slot][m.Channel]
			if same {
				c = merge(s.channels[0][m.Channel], s.channels[1][m.Channel])
			}
			c = applyServerLabel(c, labels)
			items = append(items, item{m, c})
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
		if len(f.Guilds) > 0 && !slices.Contains(f.Guilds, c.guild) || f.Channel != "" && f.Channel != m.Channel || f.Kind != "" && f.Kind != c.kind || f.Media == "attachments" && !m.HasAttachments || f.Media == "media" && !m.HasMedia || search != "" && !strings.Contains(strings.ToLower(m.Content), search) {
			continue
		}
		server := c.server
		if server == "" {
			switch c.kind {
			case "dm":
				server = "Direct messages"
			case "unknown-dm":
				server = "Unknown participants"
			default:
				server = "Unknown server"
			}
		}
		status := "observed"
		if mode == "missing" || mode == "present" || mode == "new" {
			status = mode
		}
		rows = append(rows, Row{ID: m.ID, Channel: m.Channel, Date: m.Date, Content: m.Content, Guild: c.guild, Server: server, Name: cmp.Or(c.name, m.Channel), Kind: cmp.Or(c.kind, "unknown"), Status: status, HasAttachments: m.HasAttachments, HasMedia: m.HasMedia})
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
		out = append(out, Group{k[0], k[1], n})
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
