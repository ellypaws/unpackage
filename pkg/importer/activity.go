package importer

import (
	"cmp"
	"strconv"
	"strings"
	"time"

	"github.com/ellypaws/unpackage/pkg/store"
)

var eventKinds = map[string]store.EventKind{
	"join_voice_channel":   store.EventVoiceJoin,
	"voice_disconnect":     store.EventVoice,
	"application_closed":   store.EventGame,
	"session_start":        store.EventSession,
	"add_reaction":         store.EventReaction,
	"guild_joined":         store.EventGuildJoin,
	"video_stream_started": store.EventStream,
	"message_edited":       store.EventEdit,
	"message_deleted":      store.EventDelete,
}

type activityFacts struct {
	Channel store.ChannelObservation
	Event   store.Event
	Sent    store.SentMessage
}

func activitySource(name string) store.ActivitySource {
	for part := range strings.SplitSeq(strings.ToLower(name), "/") {
		switch strings.TrimSuffix(part, ".json") {
		case "reporting":
			return store.ActivityReporting
		case "tns":
			return store.ActivityTNS
		}
	}
	return store.ActivityOther
}

func activityChannel(m map[string]string) (string, string) {
	channel := m["channel_id"]
	if !digits(channel) {
		channel = m["channel"]
	}
	guild := m["guild_id"]
	if !digits(guild) {
		guild = m["server"]
	}
	if !digits(channel) {
		channel = ""
	}
	if !digits(guild) {
		guild = ""
	}
	return channel, guild
}

func channelKind(value, guild string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "1", "DM":
		return "dm"
	case "3", "GROUP_DM":
		return "group"
	case "0", "2", "4", "5", "6", "10", "11", "12", "13", "14", "15", "16",
		"GUILD_TEXT", "GUILD_VOICE", "GUILD_CATEGORY", "GUILD_ANNOUNCEMENT", "GUILD_NEWS", "GUILD_STORE",
		"ANNOUNCEMENT_THREAD", "PUBLIC_THREAD", "PRIVATE_THREAD", "GUILD_STAGE_VOICE", "GUILD_DIRECTORY", "GUILD_FORUM", "GUILD_MEDIA":
		return "guild"
	}
	if guild != "" {
		return "guild"
	}
	return ""
}

func activityCount(v string) int {
	return int(min(integer(v), 1<<31-1))
}

func activityRecord(m map[string]string, source store.ActivitySource) activityFacts {
	channel, guild := activityChannel(m)
	kind := channelKind(m["channel_type"], guild)
	structuredKind := kind != ""
	kindRank := 2
	name := m["channel_name"]
	server := m["guild_name"]
	serverRank := 2
	title := ""
	recipients := cmp.Or(m["recipient_ids"], m["recipients"])
	if kind == "" && name != "" {
		_, inferredServer, inferredKind := label(name)
		if inferredKind != "unknown" {
			kind = inferredKind
			kindRank = 1
			if server == "" && inferredServer != "" {
				server = inferredServer
				serverRank = 1
			}
		}
	}
	if !structuredKind && strings.EqualFold(m["private"], "true") {
		switch {
		case strings.Contains(recipients, "\n"):
			kind = "group"
			kindRank = 2
		case kind == "" || kind == "unknown-dm":
			kind = "unknown-dm"
			kindRank = 2
		}
	}
	if kind == "group" {
		title = name
		name = ""
	}
	facts := activityFacts{}
	if channel != "" && (name != "" || guild != "" || server != "" || kind != "" || title != "" || recipients != "") {
		facts.Channel = store.ChannelObservation{ID: channel, Name: name, Guild: guild, Server: server, Kind: kind, Title: title, Recipients: recipients, Rank: 2, ServerRank: serverRank, KindRank: kindRank}
	}

	eventKind := eventKinds[m["event_type"]]
	if eventKind == 0 && m["event_type"] != "send_message" {
		return facts
	}
	at, hasTime := eventTime(m["timestamp"])
	client := platform(m["os"], m["browser"])
	if eventKind != 0 && hasTime {
		e := store.Event{ID: m["event_id"], Kind: eventKind, Time: at, Guild: guild, Channel: channel, Platform: client}
		if e.ID == "" {
			e.ID = m["event_type"] + "\x00" + m["timestamp"] + "\x00" + channel
		}
		switch eventKind {
		case store.EventVoice:
			// Screen-share streams report their own disconnects inside the voice session that carries them.
			if m["context"] != "stream" {
				ms := integer(m["duration_connected_ms"])
				if ms <= 0 {
					ms = integer(m["duration"])
				}
				e.Duration = time.Duration(ms) * time.Millisecond
				facts.Event = e
			}
		case store.EventGame:
			e.Name = cmp.Or(m["application_name"], m["application_id"])
			e.Duration = time.Duration(integer(m["activity_duration_s"])) * time.Second
			e.Total = time.Duration(integer(m["total_duration_s"])) * time.Second
			facts.Event = e
		case store.EventReaction:
			e.Name = m["emoji_name"]
			facts.Event = e
		default:
			facts.Event = e
		}
	}

	if m["event_type"] == "send_message" && digits(m["message_id"]) {
		eventID := m["event_id"]
		if len(eventID) > 1024 {
			eventID = ""
		}
		facts.Sent = store.SentMessage{
			ID:          m["message_id"],
			EventID:     eventID,
			Channel:     channel,
			Guild:       guild,
			Kind:        kind,
			KindRank:    kindRank,
			Time:        at,
			Platform:    client,
			Length:      activityCount(m["length"]),
			Words:       activityCount(m["word_count"]),
			URLs:        activityCount(m["num_urls"]),
			Attachments: activityCount(m["num_attachments"]),
			HasMedia:    attachmentMedia(m["attachment_content_types"], m["attachment_mimetypes"]),
			Sources:     source,
		}
	}
	return facts
}

// Analytics timestamps arrive as JSON strings that themselves contain quotes.
func eventTime(v string) (time.Time, bool) {
	v = strings.Trim(strings.TrimSpace(v), "\"")
	if v == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func integer(v string) int64 {
	whole, _, _ := strings.Cut(strings.TrimSpace(v), ".")
	n, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func platform(os, browser string) string {
	os = strings.TrimSpace(os)
	browser = strings.TrimSpace(browser)
	switch {
	case browser == "":
		return os
	case os == "" || strings.Contains(browser, os):
		return browser
	}
	return os + ", " + browser
}
