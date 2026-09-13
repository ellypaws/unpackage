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
