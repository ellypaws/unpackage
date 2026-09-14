package importer

import (
	"archive/zip"
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ellypaws/unpackage/pkg/store"
)

type file struct {
	name, channel, guild string
	size                 int64
	category             int
}

func Load(ctx context.Context, s *store.Store, slot int, p string, limiter chan struct{}, logger *slog.Logger) (ret error) {
	snapshot := store.Snapshot{Slot: slot, Path: p, State: "loading", Phase: "discovering"}
	s.SetSnapshot(slot, snapshot)
	logger.Info("Import started", "slot", slot)
	var processed atomic.Int64
	var issues atomic.Int64
	var lastPublish atomic.Int64
	var publishMu sync.Mutex
	publish := func(phase string, force bool) {
		now := time.Now().UnixNano()
		if !force {
			last := lastPublish.Load()
			if now-last < int64(150*time.Millisecond) || !lastPublish.CompareAndSwap(last, now) {
				return
			}
		} else {
			lastPublish.Store(now)
		}
		publishMu.Lock()
		defer publishMu.Unlock()
		snapshot.Phase = phase
		snapshot.Bytes = processed.Load()
		snapshot.Errors = int(issues.Load())
		s.SetSnapshot(slot, snapshot)
	}
	defer func() {
		state := "ready"
		if ret != nil {
			state = "partial"
		}
		if errors.Is(ret, context.Canceled) {
			state = "stopped"
		}
		if state == "partial" {
			snapshot.Errors = max(int(issues.Load()), 1)
			logger.Warn("Package could not be fully imported", "slot", slot)
		}
		snapshot.State = state
		snapshot.Bytes = processed.Load()
		s.SetSnapshot(slot, snapshot)
		logger.Info("Import finished", "slot", slot, "state", state)
	}()
	st, e := os.Stat(p)
	if e != nil {
		return fmt.Errorf("cannot open package")
	}
	var source fs.FS
	var closeSource func() error
	if st.IsDir() {
		root, e := os.OpenRoot(p)
		if e != nil {
			return fmt.Errorf("cannot open package directory")
		}
		source = root.FS()
		closeSource = root.Close
	} else {
		z, e := zip.OpenReader(p)
		if e != nil {
			return fmt.Errorf("cannot open package ZIP")
		}
		defer z.Close()
		seen := map[string]bool{}
		for _, f := range z.File {
			n := strings.TrimSuffix(f.Name, "/")
			if !fs.ValidPath(n) || strings.Contains(n, "\\") {
				return fmt.Errorf("unsafe ZIP entry")
			}
			low := strings.ToLower(n)
			if seen[low] {
				return fmt.Errorf("duplicate ZIP entry")
			}
			seen[low] = true
		}
		source = z
		closeSource = func() error { return nil }
	}
	defer closeSource()
	var files []file
	var total int64
	roots := map[string]bool{}
	e = fs.WalkDir(source, ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return fmt.Errorf("package enumeration failed")
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlink entries are unsupported")
		}
		if d.IsDir() {
			return nil
		}
		lower := strings.ToLower(p)
		parts := strings.Split(lower, "/")
		cat := -1
		cid := ""
		for i, part := range parts {
			if part == "messages" {
				roots[strings.Join(parts[:i], "/")] = true
				if i+1 == len(parts)-1 && parts[i+1] == "index.json" {
					cat = 1
				}
				if i+2 == len(parts)-1 {
					cid = strings.TrimPrefix(parts[i+1], "c")
					if digits(cid) {
						switch parts[i+2] {
						case "channel.json":
							cat = 2
						case "messages.json", "messages.csv":
							cat = 3
						}
					}
				}
			}
			if part == "account" && i+1 == len(parts)-1 && parts[i+1] == "user.json" {
				cat = 0
			}
			if part == "activity" && strings.HasSuffix(lower, ".json") {
				cat = 4
			}
			if part == "servers" {
				if i+1 == len(parts)-1 && parts[i+1] == "index.json" {
					cat = 5
					cid = ""
				} else if i+2 == len(parts)-1 {
					gid := parts[i+1]
					if digits(gid) && (parts[i+2] == "guild.json" || parts[i+2] == "channels.json") {
						cat = 5
						cid = ""
					}
				}
			}
		}
		if cat < 0 {
			return nil
		}
		inf, e := d.Info()
		if e != nil {
			return e
		}
		gid := ""
		if cat == 5 {
			for i, part := range parts {
				if part == "servers" && i+1 < len(parts) {
					gid = parts[i+1]
					break
				}
			}
		}
		files = append(files, file{name: p, channel: cid, guild: gid, size: inf.Size(), category: cat})
		total += inf.Size()
		return nil
	})
	if e != nil {
		return e
	}
	if len(roots) != 1 {
		return fmt.Errorf("expected exactly one package Messages directory")
	}
	slices.SortFunc(files, func(a, b file) int {
		if a.category != b.category {
			return a.category - b.category
		}
		return strings.Compare(a.name, b.name)
	})
	snapshot.Total = total
	publish("discovering", true)
	accountFiles := 0
	for _, f := range files {
		if f.category == 0 {
			accountFiles++
		}
	}
	if accountFiles > 1 {
		return fmt.Errorf("multiple account identity files; choose one package root")
	}
	jsonDirs := map[string]bool{}
	for _, f := range files {
		if f.category == 3 && strings.HasSuffix(strings.ToLower(f.name), ".json") {
			jsonDirs[path.Dir(f.name)] = true
		}
	}
	var stages [6][]file
	for _, f := range files {
		if f.category == 3 && strings.HasSuffix(strings.ToLower(f.name), ".csv") && jsonDirs[path.Dir(f.name)] {
			processed.Add(f.size)
			continue
		}
		stages[f.category] = append(stages[f.category], f)
	}
	processFile := func(f file, phase string) error {
		r, e := source.Open(f.name)
		if e != nil {
			return e
		}
		g := &guarded{ctx: ctx, r: r, csv: strings.HasSuffix(strings.ToLower(f.name), ".csv")}
		var messages []store.Message
		messageBytes := 0
		var metadata []store.ChannelObservation
		var events []store.Event
		var sentMessages []store.SentMessage
		var serverNames []store.ServerObservation
		var names map[string]store.IdentityObservation
		var lastNameID, lastUsername, lastGlobalName string
		identityRank := 1
		if f.category == 0 {
			identityRank = 3
		} else if f.category == 2 {
			identityRank = 2
		}
		identityRank = sourcePriority(identityRank, slot)
		observations := map[store.ChannelObservation]bool{}
		serverObservations := map[store.ServerObservation]bool{}
		activityOrigin := store.ActivityOther
		if f.category == 4 {
			activityOrigin = activitySource(f.name)
		}
		reported := int64(0)
		lastProgress := time.Now()
		flushMessages := func() error {
			if len(messages) == 0 {
				return nil
			}
			e := s.Add(ctx, slot, messages)
			if e == nil {
				messages = messages[:0]
				messageBytes = 0
			}
			return e
		}
		flushMetadata := func() error {
			if len(metadata) == 0 {
				return nil
			}
			e := s.Channels(ctx, slot, metadata)
			if e == nil {
				metadata = metadata[:0]
			}
			return e
		}
		flushEvents := func() error {
			if len(events) == 0 {
				return nil
			}
			e := s.Events(ctx, slot, events)
			if e == nil {
				events = events[:0]
			}
			return e
		}
		flushSentMessages := func() error {
			if len(sentMessages) == 0 {
				return nil
			}
			e := s.SentMessages(ctx, slot, sentMessages)
			if e == nil {
				sentMessages = sentMessages[:0]
			}
			return e
		}
		flushServerNames := func() error {
			if len(serverNames) == 0 {
				return nil
			}
			e := s.ServerNames(ctx, slot, serverNames)
			if e == nil {
				serverNames = serverNames[:0]
			}
			return e
		}
		collectServer := func(observation store.ServerObservation) error {
			if observation.ID == "" || observation.Name == "" || serverObservations[observation] {
				return nil
			}
			if len(serverObservations) >= 16384 {
				clear(serverObservations)
			}
			serverObservations[observation] = true
			serverNames = append(serverNames, observation)
			if len(serverNames) >= 512 {
				return flushServerNames()
			}
			return nil
		}
		collectName := func(m map[string]string) error {
			id := m["id"]
			if id == "" {
				return nil
			}
			username, globalName := m["username"], m["global_name"]
			if (username == "" && globalName == "") || (id == lastNameID && username == lastUsername && globalName == lastGlobalName) {
				return nil
			}
			current := names[id]
			if !digits(id) || (current == (store.IdentityObservation{}) && len(names) >= 1<<16) {
				return nil
			}
			if names == nil {
				names = make(map[string]store.IdentityObservation)
			}
			if username != "" {
				current.Username = username
				current.Aliases = addIdentityAlias(current.Aliases, username)
			}
			if globalName != "" {
				current.GlobalName = globalName
				current.Aliases = addIdentityAlias(current.Aliases, globalName)
			}
			current.Rank = identityRank
			names[id] = current
			lastNameID, lastUsername, lastGlobalName = id, username, globalName
			return nil
		}
		progress := func() error {
			if time.Since(lastProgress) < 150*time.Millisecond {
				return ctx.Err()
			}
			delta := g.bytes - reported
			if delta > 0 {
				processed.Add(delta)
				reported = g.bytes
			}
			if e := flushMessages(); e != nil {
				return e
			}
			if e := flushMetadata(); e != nil {
				return e
			}
			if e := flushEvents(); e != nil {
				return e
			}
			if e := flushSentMessages(); e != nil {
				return e
			}
			if e := flushServerNames(); e != nil {
				return e
			}
			publish(phase, false)
			lastProgress = time.Now()
			return ctx.Err()
		}
		record := func(m map[string]string) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			if e := progress(); e != nil {
				return e
			}
			switch f.category {
			case 0:
				if digits(m["id"]) {
					publishMu.Lock()
					snapshot.Owner = m["id"]
					publishMu.Unlock()
					publish(phase, true)
					return nil
				}
			case 2:
				if m["id"] != "" && m["id"] != f.channel {
					return fmt.Errorf("channel metadata ID does not match its directory")
				}
				gid := m["guild_id"]
				if gid == "" && digits(m["guild"]) {
					gid = m["guild"]
				}
				kind := channelKind(m["type"], gid)
				if kind == "" {
					kind = "unknown"
				}
				observation := store.ChannelObservation{ID: f.channel, Name: m["name"], Guild: gid, Server: m["guild_name"], Kind: kind, Recipients: m["recipients"], Rank: sourcePriority(3, slot)}
				if kind == "group" {
					observation = store.ChannelObservation{ID: f.channel, Kind: "group", Title: m["name"], Recipients: m["recipients"], Rank: sourcePriority(3, slot)}
				}
				metadata = append(metadata, observation)
			case 3:
				id := m["id"]
				if id == "" {
					return fmt.Errorf("message record has no ID")
				}
				if !digits(id) {
					return fmt.Errorf("invalid message ID")
				}
				body := m["contents"]
				if body == "" {
					body = m["content"]
				}
				hasAttachments := m["has_attachments"] == "1" || attachmentPresent(m["attachments"])
				hasMedia := m["has_media"] == "1" || attachmentMedia(m["attachments"])
				attachmentURLs := attachmentURLs(m["attachments"], m[attachmentURLsKey])
				var features []string
				for _, kind := range store.SearchHas {
					if m["has_"+kind] == "1" {
						features = append(features, kind)
					}
				}
				messages = append(messages, store.Message{ID: id, Channel: f.channel, Date: store.Day(id), Content: body, Features: features, AttachmentURLs: attachmentURLs, HasAttachments: hasAttachments, HasMedia: hasMedia})
				messageBytes += len(body)
				for _, attachmentURL := range attachmentURLs {
					messageBytes += len(attachmentURL)
				}
				if len(messages) >= 256 || messageBytes >= 2<<20 {
					return flushMessages()
				}
			case 4:
				if e := collectName(m); e != nil {
					return e
				}
				facts := activityRecord(m, activityOrigin)
				facts.Channel.Rank = sourcePriority(facts.Channel.Rank, slot)
				facts.Channel.GuildRank = sourcePriority(facts.Channel.GuildRank, slot)
				facts.Channel.ServerRank = sourcePriority(facts.Channel.ServerRank, slot)
				facts.Channel.KindRank = sourcePriority(facts.Channel.KindRank, slot)
				facts.Server.Rank = sourcePriority(facts.Server.Rank, slot)
				facts.Sent.KindRank = sourcePriority(facts.Sent.KindRank, slot)
				if e := collectServer(facts.Server); e != nil {
					return e
				}
				if observation := facts.Channel; observation.ID != "" {
					if !observations[observation] {
						if len(observations) >= 16384 {
							clear(observations)
						}
						observations[observation] = true
						metadata = append(metadata, observation)
						if len(metadata) >= 512 {
							if e := flushMetadata(); e != nil {
								return e
							}
						}
					}
				}
				if facts.Event.Kind != 0 {
					events = append(events, facts.Event)
					if len(events) >= 512 {
						return flushEvents()
					}
				}
				if facts.Sent.ID != "" {
					sentMessages = append(sentMessages, facts.Sent)
					if len(sentMessages) >= 512 {
						return flushSentMessages()
					}
				}
			case 5:
				switch path.Base(strings.ToLower(f.name)) {
				case "guild.json":
					id := cmp.Or(m["id"], f.guild)
					if m["id"] != "" && m["id"] != f.guild {
						return fmt.Errorf("server metadata ID does not match its directory")
					}
					if digits(id) && m["name"] != "" {
						if e := collectServer(store.ServerObservation{ID: id, Name: m["name"], Rank: sourcePriority(4, slot)}); e != nil {
							return e
						}
					}
				case "channels.json":
					id := m["id"]
					kind := channelKind(m["type"], f.guild)
					if digits(id) && m["name"] != "" && kind == "guild" {
						priority := sourcePriority(4, slot)
						metadata = append(metadata, store.ChannelObservation{ID: id, Name: m["name"], Guild: f.guild, Kind: kind, Rank: priority, KindRank: priority})
					}
				}
			}
			return nil
		}
		field := func(k, v string) error {
			if f.category == 1 && digits(k) {
				name, guild, kind := store.ClassifyChannelLabel(v)
				metadata = append(metadata, store.ChannelObservation{ID: k, Name: name, Server: guild, Kind: kind, Rank: sourcePriority(1, slot)})
				if len(metadata) >= 512 {
					return flushMetadata()
				}
			}
			if f.category == 5 && path.Base(strings.ToLower(f.name)) == "index.json" && digits(k) && v != "" {
				return collectServer(store.ServerObservation{ID: k, Name: v, Rank: sourcePriority(3, slot)})
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(f.name), ".csv") {
			e = readCSV(g, record)
		} else if f.category == 3 {
			e = readMessages(g, record)
		} else if f.category == 0 || f.category == 2 || f.category == 5 && path.Base(strings.ToLower(f.name)) == "guild.json" {
			d := json.NewDecoder(g)
			d.UseNumber()
			var root map[string]string
			object := collectName
			if f.category == 5 {
				object = nil
			}
			root, e = walk(d, 0, object, nil)
			if e == nil {
				e = record(root)
			}
			if e == nil && len(names) > 0 {
				e = s.Names(ctx, slot, names)
			}
			if e == nil {
				_, e = d.Token()
				if e == io.EOF {
					e = nil
				} else if e == nil {
					e = fmt.Errorf("trailing JSON data")
				}
			}
			if e != nil && f.category == 0 {
				snapshot.Owner = ""
				s.SetSnapshot(slot, snapshot)
			}
		} else if f.category == 4 {
			d := json.NewDecoder(g)
			d.UseNumber()
			for {
				_, e = walk(d, 0, record, field)
				if e == io.EOF {
					e = nil
					break
				}
				if e != nil {
					break
				}
			}
		} else {
			e = stream(g, record, field)
		}
		if e == nil && f.category == 4 && len(names) > 0 {
			e = s.Names(ctx, slot, names)
		}
		e = errors.Join(e, flushMessages(), flushMetadata(), flushEvents(), flushSentMessages(), flushServerNames(), r.Close())
		if delta := g.bytes - reported; delta > 0 {
			processed.Add(delta)
		}
		publish(phase, false)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return e
	}
	runStage := func(category int) (int, error) {
		started := time.Now()
		phase := []string{"account", "index", "channels", "messages", "activity", "servers"}[category]
		publish(phase, true)
		results := parallelFiles(ctx, limiter, stages[category], func(f file) error { return processFile(f, phase) })
		if e := ctx.Err(); e != nil {
			return 0, e
		}
		failed := 0
		for _, result := range results {
			if result == nil {
				continue
			}
			failed++
			issues.Add(1)
			logger.Warn("Import file incomplete", "slot", slot, "phase", phase)
		}
		publish(phase, true)
		logger.Info("Import phase finished", "slot", slot, "phase", phase, "elapsed_ms", time.Since(started).Milliseconds(), "files", len(stages[category]))
		return failed, nil
	}
	failed, e := runStage(0)
	if e != nil {
		return e
	}
	if failed > 0 {
		snapshot.Owner = ""
	}
	hasMessages := len(stages[3]) > 0
	messageFailures, e := runStage(3)
	if e != nil {
		return e
	}
	if hasMessages && messageFailures == 0 {
		snapshot.Complete = true
		publish("messages", true)
	}
	for _, category := range []int{1, 2, 5} {
		if _, e := runStage(category); e != nil {
			return e
		}
	}
	publish("waiting for messages", true)
	if e := s.WaitMessages(ctx); e != nil {
		return e
	}
	if _, e = runStage(4); e != nil {
		return e
	}
	if accountFiles != 1 {
		snapshot.Owner = ""
		s.SetSnapshot(slot, snapshot)
	}
	if !hasMessages {
		return fmt.Errorf("no supported message files")
	}
	if count := issues.Load(); count > 0 {
		return fmt.Errorf("%d files could not be fully imported; valid rows retained", count)
	}
	return nil
}

func parallelFiles(ctx context.Context, limiter chan struct{}, files []file, fn func(file) error) []error {
	results := make([]error, len(files))
	if len(files) == 0 {
		return results
	}
	workers := min(len(files), max(1, cap(limiter)))
	var next atomic.Int64
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			for {
				i := int(next.Add(1) - 1)
				if i >= len(files) {
					return
				}
				select {
				case limiter <- struct{}{}:
				case <-ctx.Done():
					results[i] = ctx.Err()
					return
				}
				results[i] = fn(files[i])
				<-limiter
			}
		})
	}
	group.Wait()
	return results
}

func readMessages(r io.Reader, fn func(map[string]string) error) error {
	d := json.NewDecoder(r)
	d.UseNumber()
	t, e := d.Token()
	if e != nil {
		return e
	}
	if t != json.Delim('[') {
		return fmt.Errorf("messages must be a JSON array")
	}
	for d.More() {
		m, e := walk(d, 0, nil, nil)
		if e != nil {
			return e
		}
		if e = fn(m); e != nil {
			return e
		}
	}
	if _, e = d.Token(); e != nil {
		return e
	}
	_, e = d.Token()
	if e == io.EOF {
		return nil
	}
	if e == nil {
		return fmt.Errorf("trailing message data")
	}
	return e
}

func addIdentityAlias(aliases, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return aliases
	}
	for alias := range strings.SplitSeq(aliases, "\n") {
		if strings.EqualFold(alias, value) {
			return aliases
		}
	}
	if aliases == "" {
		return value
	}
	if len(aliases)+1+len(value) > 4096 {
		return aliases
	}
	return aliases + "\n" + value
}

func sourcePriority(level, slot int) int {
	if level <= 0 {
		return 0
	}
	return level*2 + slot
}

func readCSV(r io.Reader, fn func(map[string]string) error) error {
	c := csv.NewReader(r)
	h, e := c.Read()
	if e != nil {
		return e
	}
	hasID := false
	for i, v := range h {
		h[i] = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(v), "\ufeff"))
		hasID = hasID || h[i] == "id"
	}
	if !hasID {
		return fmt.Errorf("missing CSV ID column")
	}
	for {
		row, e := c.Read()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		m := map[string]string{}
		for i, v := range row {
			m[h[i]] = v
		}
		if e = fn(m); e != nil {
			return e
		}
	}
}
