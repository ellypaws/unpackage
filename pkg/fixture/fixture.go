package fixture

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func Generate(dir string, count int) error {
	if count < 4 || count > 10000000 {
		return fmt.Errorf("sample count must be between 4 and 10000000")
	}
	if _, e := os.Stat(dir); e == nil {
		return fmt.Errorf("sample destination already exists")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	for _, slot := range []string{"older", "newer"} {
		base := filepath.Join(dir, slot)
		write := func(name string, v any) error {
			p := filepath.Join(base, filepath.FromSlash(name))
			if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
				return e
			}
			f, e := os.Create(p)
			if e != nil {
				return e
			}
			e = json.NewEncoder(f).Encode(v)
			return errorsClose(f, e)
		}
		account := map[string]any{"id": "100000000000000001", "username": "synthetic", "relationships": []any{
			map[string]any{"user": map[string]string{"id": "100000000000000002", "username": "sample_a", "global_name": "Sample A"}},
			map[string]any{"user": map[string]string{"id": "100000000000000003", "username": "sample_b", "global_name": "Sample B"}},
		}}
		if e := write("Account/user.json", account); e != nil {
			return e
		}
		index := map[string]any{"200000000000000001": "general in Sample Garden", "200000000000000002": "thread with spaces in Sample Workshop", "200000000000000003": "Direct Message with Sample#0", "200000000000000004": "Direct Message with Unknown Participant", "200000000000000005": nil}
		if e := write("Messages/index.json", index); e != nil {
			return e
		}
		for ch := 1; ch <= 5; ch++ {
			cid := fmt.Sprintf("20000000000000000%d", ch)
			name := "general"
			if ch == 2 {
				name = "thread with spaces"
			}
			meta := map[string]any{"id": cid, "type": "GUILD_TEXT", "name": name}
			if ch == 1 {
				meta["guild"] = map[string]string{"id": "300000000000000001", "name": "Sample Garden"}
			}
			if ch == 3 || ch == 4 {
				meta["type"] = "DM"
				delete(meta, "name")
			}
			if ch == 5 {
				meta["type"] = "GROUP_DM"
				meta["name"] = nil
				meta["recipients"] = []string{"100000000000000002", "100000000000000003", "Deleted User"}
			}
			if e := write("Messages/c"+cid+"/channel.json", meta); e != nil {
				return e
			}
			p := filepath.Join(base, "Messages", "c"+cid, "messages.json")
			f, e := os.Create(p)
			if e != nil {
				return e
			}
			_, e = io.WriteString(f, "[")
			first := true
			for i := ch - 1; i < count; i += 5 {
				if slot == "newer" && i%4 == 0 {
					continue
				}
				if !first {
					_, e = io.WriteString(f, ",")
				}
				first = false
				ts := time.Date(2022, 11, 15, 12, 0, 0, 0, time.UTC).AddDate(0, 0, i%30)
				id := strconv.FormatUint(uint64(ts.UnixMilli()-1420070400000)<<22|uint64(i), 10)
				text := fmt.Sprintf("Synthetic message %d", i)
				if slot == "newer" && i%4 == 1 {
					text += " edited"
				}
				row := map[string]string{"ID": id, "Timestamp": ts.Format(time.RFC3339), "Contents": text, "Attachments": "https://example.invalid/file.png?ex=" + slot}
				if e = json.NewEncoder(f).Encode(row); e != nil {
					break
				}
			}
			if e == nil {
				_, e = io.WriteString(f, "]")
			}
			if e = errorsClose(f, e); e != nil {
				return e
			}
		}
		activity := []any{map[string]any{"event_type": "channel_opened", "properties": map[string]string{"channel_id": "200000000000000002", "channel_name": "thread with spaces", "guild_id": "300000000000000002", "guild_name": "Sample Workshop"}}}
		for i := range 12 {
			at := time.Date(2022, 11, 15, 18, 0, 0, 0, time.UTC).AddDate(0, 0, i*2)
			stamp := func(offset time.Duration) string { return at.Add(offset).Format(time.RFC3339) }
			activity = append(activity,
				map[string]any{"event_type": "join_voice_channel", "event_id": fmt.Sprintf("voice-join-%d", i), "timestamp": stamp(0), "channel_id": "200000000000000001", "guild_id": "300000000000000001", "os": "Windows", "browser": "Discord Client"},
				map[string]any{"event_type": "voice_disconnect", "event_id": fmt.Sprintf("voice-%d", i), "timestamp": stamp(90 * time.Minute), "channel_id": "200000000000000001", "guild_id": "300000000000000001", "duration_connected_ms": "5400000", "os": "Windows", "browser": "Discord Client"},
				map[string]any{"event_type": "application_closed", "event_id": fmt.Sprintf("game-%d", i), "timestamp": stamp(4 * time.Hour), "application_name": "Sample Quest", "activity_duration_s": "3600", "total_duration_s": strconv.Itoa(3600 * (i + 1)), "os": "Windows", "browser": "Discord Client"},
				map[string]any{"event_type": "session_start", "event_id": fmt.Sprintf("session-%d", i), "timestamp": stamp(-2 * time.Hour), "os": "iOS", "browser": "Discord iOS"},
				map[string]any{"event_type": "add_reaction", "event_id": fmt.Sprintf("reaction-%d", i), "timestamp": stamp(time.Hour), "channel_id": "200000000000000002", "guild_id": "300000000000000002", "emoji_name": "sparkles"},
			)
		}
		if e := write("Activity/tns/events.json", activity); e != nil {
			return e
		}
		if e := archive(base, filepath.Join(dir, slot+".zip")); e != nil {
			return e
		}
	}
	return safety(dir)
}

// safety writes fictional Safety Hub and system DM responses that point at sample messages.
func safety(dir string) error {
	at := func(i int) time.Time { return time.Date(2022, 11, 15, 12, 0, 0, 0, time.UTC).AddDate(0, 0, i%30) }
	snowflake := func(t time.Time, n int) string {
		return strconv.FormatUint(uint64(t.UnixMilli()-1420070400000)<<22|uint64(n), 10)
	}
	hate, spam, guild, removed := snowflake(at(0).Add(5*time.Minute), 1), snowflake(at(7).Add(9*time.Minute), 2), snowflake(at(12), 3), snowflake(at(20).Add(time.Hour), 4)
	expires := func(t time.Time) *string { return new(t.Format(time.RFC3339Nano)) }
	action := func(id string, kind int, text string) map[string]any {
		return map[string]any{"id": id, "action_type": kind, "descriptions": []string{text}}
	}
	hub := map[string]any{
		"classifications": []any{
			map[string]any{"id": hate, "classification_type": 220, "description": "Hateful conduct", "explainer_link": "https://example.invalid/policy/hateful-conduct", "actions": []any{action(snowflake(at(0), 11), 4, "A warning was added to the account."), action(snowflake(at(0), 12), 16, "The message was removed.")}, "max_expiration_time": expires(at(0).AddDate(1, 0, 0)), "flagged_content": []any{map[string]any{"type": "message", "id": snowflake(at(0), 0), "content": "Synthetic message 0", "attachments": []any{map[string]string{"filename": "file.png"}}}}, "appeal_status": map[string]int{"status": 1}, "is_coppa": false, "is_spam": false, "appeal_ingestion_type": 2},
			map[string]any{"id": spam, "classification_type": 3030, "description": "", "explainer_link": "https://example.invalid/policy/spam", "actions": []any{action(snowflake(at(7), 13), 7, "The message was marked as spam.")}, "max_expiration_time": expires(at(7).AddDate(0, 6, 0)), "flagged_content": []any{}, "appeal_status": map[string]int{"status": 2}, "is_coppa": false, "is_spam": true, "appeal_ingestion_type": 0},
			map[string]any{"id": removed, "classification_type": 5411, "description": "Minimum age requirement", "explainer_link": "https://example.invalid/policy/age", "actions": []any{action(snowflake(at(20), 14), 9, "Some features were limited.")}, "max_expiration_time": nil, "flagged_content": []any{}, "appeal_status": map[string]int{"status": 3}, "is_coppa": true, "is_spam": false, "appeal_ingestion_type": 1},
		},
		"guild_classifications": []any{
			map[string]any{"id": guild, "classification_type": 5305, "description": "Member of a server that shared personal information", "explainer_link": "https://example.invalid/policy/doxxing", "actions": []any{action(snowflake(at(12), 15), 15, "Server access was limited.")}, "max_expiration_time": expires(at(12).AddDate(0, 1, 0)), "flagged_content": []any{}, "is_coppa": false, "is_spam": false, "appeal_ingestion_type": 0, "guild_metadata": map[string]any{"name": "Sample Workshop", "icon": nil, "member_type": 2}},
		},
		"account_standing":   map[string]int{"state": 200},
		"is_dsa_eligible":    true,
		"is_appeal_eligible": true,
		"username":           "synthetic",
		"appeal_eligibility": []int{2, 1},
	}
	field := func(name, value string) map[string]any {
		return map[string]any{"name": name, "value": value, "inline": false}
	}
	notice := func(id, classification string, incident time.Time) map[string]any {
		return map[string]any{"id": id, "type": 0, "content": "", "embeds": []any{map[string]any{"type": "safety_policy_notice", "title": "You broke Discord's community guidelines", "fields": []any{
			field("client_version_message", "To see the details of this violation, please update the app or open Discord in your browser."),
			field("classification_id", classification),
			field("incident_time", strconv.FormatInt(incident.Unix(), 10)+".0"),
		}}}}
	}
	notices := []any{
		map[string]any{"id": snowflake(at(21), 23), "type": 0, "content": "", "embeds": []any{map[string]any{"type": "safety_system_notification", "title": "Important message from Discord regarding your account", "fields": []any{
			field("body", "We reviewed a violation regarding our minimum age requirements policy and determined it does not violate our community guidelines."),
			field("header", "We have removed a violation from your account"),
			field("timestamp", strconv.FormatInt(at(21).Unix(), 10)+".703625"),
			field("classification_id", removed),
		}}}},
		notice(snowflake(at(7).Add(10*time.Minute), 22), spam, at(7)),
		notice(snowflake(at(0).Add(6*time.Minute), 21), hate, at(0)),
	}
	for name, v := range map[string]any{"safety-hub.json": hub, "safety-notices.json": notices} {
		data, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(dir, name), data, 0600); e != nil {
			return e
		}
	}
	return nil
}
func errorsClose(f *os.File, e error) error {
	closeErr := f.Close()
	if e != nil {
		return e
	}
	return closeErr
}
func archive(src, dst string) error {
	f, e := os.Create(dst)
	if e != nil {
		return e
	}
	z := zip.NewWriter(f)
	e = filepath.WalkDir(src, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(src, p)
		if e != nil {
			return e
		}
		w, e := z.Create("package/" + filepath.ToSlash(rel))
		if e != nil {
			return e
		}
		r, e := os.Open(p)
		if e != nil {
			return e
		}
		defer r.Close()
		_, e = io.Copy(w, r)
		return e
	})
	ze := z.Close()
	fe := f.Close()
	if e != nil {
		return e
	}
	if ze != nil {
		return ze
	}
	return fe
}
