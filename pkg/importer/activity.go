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

func activityEvent(m map[string]string) (store.Event, bool) {
	kind := eventKinds[m["event_type"]]
	if kind == 0 {
		return store.Event{}, false
	}
	at, ok := eventTime(m["timestamp"])
	if !ok {
		return store.Event{}, false
	}
	e := store.Event{ID: m["event_id"], Kind: kind, Time: at, Platform: platform(m["os"], m["browser"])}
	if e.ID == "" {
		e.ID = m["event_type"] + "\x00" + m["timestamp"] + "\x00" + m["channel_id"]
	}
	if digits(m["guild_id"]) {
		e.Guild = m["guild_id"]
	}
	if digits(m["channel_id"]) {
		e.Channel = m["channel_id"]
	} else if digits(m["channel"]) {
		e.Channel = m["channel"]
	}
	switch kind {
	case store.EventVoice:
		// Screen-share streams report their own disconnects inside the voice session that carries them.
		if m["context"] == "stream" {
			return store.Event{}, false
		}
		ms := integer(m["duration_connected_ms"])
		if ms <= 0 {
			ms = integer(m["duration"])
		}
		e.Duration = time.Duration(ms) * time.Millisecond
	case store.EventGame:
		e.Name = cmp.Or(m["application_name"], m["application_id"])
		e.Duration = time.Duration(integer(m["activity_duration_s"])) * time.Second
		e.Total = time.Duration(integer(m["total_duration_s"])) * time.Second
	case store.EventReaction:
		e.Name = m["emoji_name"]
	}
	return e, true
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
