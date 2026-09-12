package session

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ellypaws/unpackage/pkg/fixture"
	"github.com/ellypaws/unpackage/pkg/store"
)

const Help = `Discord package viewer

open older|newer "folder or zip"
wait                       Wait for imports
stop                       Stop loading
status                     Import and identity status
mode auto|missing|all|older|newer|present|new
servers                    List observed servers
select ID [ID ...]          Filter several servers
select all                 Clear server selection
dates "2022-11-19; 1,396 days ago"
dates clear                Clear selected days
margin BEFORE AFTER        Include days around each selected date
last N                     Last N days, including today
before YYYY-MM-DD          Before creation date
search "text"              Search message contents
channel ID                 Filter one channel
kind guild|dm|unknown-dm|unknown|conflict|all
media all|attachments|media
clear                      Reset filters
list [jsonl|tsv]            Stream all matching rows
show MESSAGE_ID            Full message details
stats                      Counts by server
days                       Counts by creation date
request "draft.txt" all|filtered
help                       Command reference
quit                       Exit

CLI: package diff OLDER NEWER [options]
  --server ID   --date "days or date"   --search TEXT
  --media all|attachments|media
  --before-days N   --after-days N
  --mode MODE   --format jsonl|tsv
  --request FILE   --scope all|filtered
package repl
package sample DIRECTORY [MESSAGE_COUNT]

Dates use local time. Quote paths with spaces.
Use Read from clipboard in Investigate to filter exact incident seconds.
Request requires selected server IDs; all ignores other filters.
Missing means absent from the newer export, not proof of deletion.`

func StyledHelp() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#B59AF6"))
	command := lipgloss.NewStyle().Foreground(lipgloss.Color("#8FBAFF"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#ADA7BD"))
	var out []string
	for i, line := range strings.Split(Help, "\n") {
		if i == 0 {
			out = append(out, title.Render(line))
			continue
		}
		left, right, found := strings.Cut(line, "  ")
		if found {
			out = append(out, command.Render(left)+dim.Render("  "+right))
		} else if line != "" {
			out = append(out, command.Render(line))
		} else {
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}

func HelpPanel(height int) string {
	heading := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#B59AF6"))
	cmd := lipgloss.NewStyle().Foreground(lipgloss.Color("#8FBAFF"))
	if height < 20 {
		return heading.Render("Quick reference") + "\n\n" + cmd.Render("search \"text\"") + "\n" + cmd.Render("dates \"1,396 days ago\"") + "\n" + cmd.Render("select SERVER_ID") + "\n" + cmd.Render("request \"draft.txt\" all") + "\n\n" + cmd.Render("help") + "  All commands"
	}
	return heading.Render("Quick reference") + "\n\n" + cmd.Render("search \"text\"") + "\nFind messages\n\n" + cmd.Render("dates \"1,396 days ago\"") + "\nFilter by day\n\n" + cmd.Render("select SERVER_ID") + "\nChoose servers\n\n" + cmd.Render("request \"draft.txt\" all") + "\nSave a deletion request\n\n" + cmd.Render("help") + "\nAll commands"
}
func (s *Session) Execute(ctx context.Context, a []string, w io.Writer) error {
	if len(a) == 0 {
		return nil
	}
	arg := strings.Join(a[1:], " ")
	need := func(n int) error {
		if len(a) != n {
			return fmt.Errorf("invalid arguments; see help")
		}
		return nil
	}
	switch a[0] {
	case "help", "--help", "-h":
		_, e := fmt.Fprintln(w, StyledHelp())
		return e
	case "open":
		if e := need(3); e != nil {
			return e
		}
		slot := -1
		if a[1] == "older" {
			slot = 0
		}
		if a[1] == "newer" {
			slot = 1
		}
		if e := s.Open(ctx, slot, a[2]); e != nil {
			return e
		}
		fmt.Fprintln(w, "Import started")
	case "wait":
		return s.Wait(ctx)
	case "stop":
		s.Stop()
		fmt.Fprintln(w, "Stopping…")
	case "status":
		ss, e := s.Store.Snapshots(ctx)
		if e != nil {
			return e
		}
		for _, v := range ss {
			fmt.Fprintf(w, "%s: %s / %s, %d messages, %d/%d bytes, %d issues\n", []string{"older", "newer"}[v.Slot], v.State, v.Phase, v.Count, v.Bytes, v.Total, v.Errors)
		}
		_, reason := s.Store.Compatible(ctx)
		fmt.Fprintln(w, reason)
	case "mode":
		if e := need(2); e != nil {
			return e
		}
		if !slices.Contains([]string{"auto", "missing", "all", "older", "newer", "present", "new"}, arg) {
			return fmt.Errorf("unknown mode")
		}
		s.Filter.Mode = arg
		s.Filter.Offset = 0
	case "select":
		if len(a) < 2 {
			return fmt.Errorf("specify server IDs or all")
		}
		if arg == "all" {
			s.Filter.Guilds = nil
		} else {
			for _, id := range a[1:] {
				if _, e := strconv.ParseUint(id, 10, 64); e != nil {
					return fmt.Errorf("server selection requires numeric IDs")
				}
			}
			s.Filter.Guilds = slices.Clone(a[1:])
		}
		s.Filter.Offset = 0
	case "dates":
		if arg == "clear" {
			s.Filter.Dates = nil
			s.Filter.IncidentSeconds = nil
		} else {
			d, e := Dates(arg, s.Today)
			if e != nil {
				return e
			}
			s.Filter.Dates = d
			s.Filter.IncidentSeconds = nil
		}
		s.Filter.From = ""
		s.Filter.Until = ""
		s.Filter.Offset = 0
	case "last":
		n, e := strconv.Atoi(arg)
		if e != nil || n < 1 || n > 100000 {
			return fmt.Errorf("last requires 1 to 100000 days")
		}
		s.Filter.Dates = nil
		s.Filter.IncidentSeconds = nil
		s.Filter.From = s.Today.AddDate(0, 0, 1-n).Format(time.DateOnly)
		s.Filter.Until = s.Today.AddDate(0, 0, 1).Format(time.DateOnly)
	case "margin":
		if e := need(3); e != nil {
			return e
		}
		before, e := Margin(a[1])
		if e != nil {
			return e
		}
		after, e := Margin(a[2])
		if e != nil {
			return e
		}
		s.Filter.DateBefore = before
		s.Filter.DateAfter = after
	case "before":
		if _, e := time.Parse(time.DateOnly, arg); e != nil {
			return e
		}
		s.Filter.Dates = nil
		s.Filter.IncidentSeconds = nil
		s.Filter.From = ""
		s.Filter.Until = arg
	case "search":
		s.Filter.Search = arg
		s.Filter.Offset = 0
	case "channel":
		s.Filter.Channel = arg
		s.Filter.Offset = 0
	case "kind":
		if !slices.Contains([]string{"guild", "dm", "unknown-dm", "unknown", "conflict", "all"}, arg) {
			return fmt.Errorf("unknown channel kind")
		}
		s.Filter.Kind = arg
		if arg == "all" {
			s.Filter.Kind = ""
		}
	case "media":
		if !slices.Contains([]string{"all", "attachments", "media"}, arg) {
			return fmt.Errorf("media must be all, attachments or media")
		}
		s.Filter.Media = arg
		if arg == "all" {
			s.Filter.Media = ""
		}
	case "clear":
		s.Filter = store.Filter{Mode: "auto", Limit: 50}
	case "list":
		if arg == "" {
			arg = "jsonl"
		}
		f := s.Filter
		f.Limit = 0
		f.Offset = 0
		return s.Output(ctx, f, arg, w)
	case "show":
		f := s.Filter
		f.Limit = 0
		f.Offset = 0
		found := false
		e := s.Store.Each(ctx, f, func(r store.Row) error {
			if r.ID == arg {
				found = true
				return json.NewEncoder(w).Encode(r)
			}
			return nil
		})
		if e != nil {
			return e
		}
		if !found {
			return fmt.Errorf("message not found in current selection")
		}
	case "servers", "stats", "days":
		f := s.Filter
		if a[0] == "servers" {
			f = store.Filter{Mode: "all"}
		}
		gs, e := s.Store.Groups(ctx, f, a[0] == "days")
		if e != nil {
			return e
		}
		for _, g := range gs {
			fmt.Fprintf(w, "%s\t%s\t%d\n", g.ID, Safe(g.Name), g.Count)
		}
	case "request":
		if e := need(3); e != nil {
			return e
		}
		n, e := s.Request(ctx, a[1], a[2])
		if e != nil {
			return e
		}
		fmt.Fprintf(w, "Saved draft with %d message IDs\n", n)
	case "sample":
		if len(a) < 2 || len(a) > 3 {
			return fmt.Errorf("sample DIRECTORY [COUNT]")
		}
		n := 80
		if len(a) == 3 {
			var e error
			n, e = strconv.Atoi(a[2])
			if e != nil {
				return e
			}
		}
		return fixture.Generate(a[1], n)
	case "quit", "exit":
		return nil
	default:
		return fmt.Errorf("unknown command %q; see help", a[0])
	}
	return nil
}
func Margin(text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 || n > 3650 {
		return 0, fmt.Errorf("margin must be between 0 and 3650 days")
	}
	return n, nil
}
func (s *Session) Output(ctx context.Context, f store.Filter, format string, w io.Writer) error {
	if format == "jsonl" {
		enc := json.NewEncoder(w)
		return s.Store.Each(ctx, f, func(r store.Row) error { return enc.Encode(r) })
	}
	if format != "tsv" {
		return fmt.Errorf("format must be jsonl or tsv")
	}
	c := csv.NewWriter(w)
	c.Comma = '\t'
	if e := c.Write([]string{"server_id", "server", "channel_id", "channel", "message_id", "date", "status", "has_attachments", "has_media", "content"}); e != nil {
		return e
	}
	e := s.Store.Each(ctx, f, func(r store.Row) error {
		return c.Write([]string{r.Guild, Safe(r.Server), r.Channel, Safe(r.Name), r.ID, r.Date, r.Status, strconv.FormatBool(r.HasAttachments), strconv.FormatBool(r.HasMedia), Safe(r.Content)})
	})
	c.Flush()
	if e != nil {
		return e
	}
	return c.Error()
}
func Safe(v string) string {
	v = ansi.Strip(v)
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || (r >= 128 && r <= 159) || r == '\u202e' || r == '\u202d' || (r >= '\u2066' && r <= '\u2069') {
			return ' '
		}
		return r
	}, v)
}

func (s *Session) Request(ctx context.Context, dest, scope string) (int, error) {
	if s.Busy() {
		return 0, fmt.Errorf("wait for imports before exporting a request")
	}
	if len(s.Filter.Guilds) == 0 {
		return 0, fmt.Errorf("select at least one server ID")
	}
	ss, e := s.Store.Snapshots(ctx)
	if e != nil {
		return 0, e
	}
	if len(ss) == 0 {
		return 0, fmt.Errorf("open a package first")
	}
	owner := ss[0].Owner
	for _, v := range ss {
		if owner == "" || v.Owner != owner || !v.Complete || v.State != "ready" {
			return 0, fmt.Errorf("request requires complete imports from one verified account")
		}
	}
	f := s.Filter
	f.Limit = 0
	f.Offset = 0
	if scope == "all" {
		groups, e := s.Store.Groups(ctx, store.Filter{Mode: "all"}, false)
		if e != nil {
			return 0, e
		}
		for _, id := range f.Guilds {
			if !slices.ContainsFunc(groups, func(g store.Group) bool { return g.ID == id }) {
				return 0, fmt.Errorf("selected server %s has no resolved message IDs", id)
			}
		}
		f = store.Filter{Mode: "all", Guilds: slices.Clone(s.Filter.Guilds)}
	} else if scope != "filtered" {
		return 0, fmt.Errorf("request scope must be all or filtered")
	}
	file, e := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return 0, e
	}
	success := false
	defer func() {
		file.Close()
		if !success {
			os.Remove(dest)
		}
	}()
	_, e = fmt.Fprintf(file, "Subject: Request for erasure of message data under GDPR Article 17\n\nTo Discord Privacy,\n\nI request erasure of my personal data in the messages identified below, insofar as Article 17 applies. My account ID is %s. This request is limited to the listed messages and does not request deletion of my account. Please confirm the action taken, or explain any applicable retention grounds and the available review process.\n\nThe IDs come from my Discord data exports. Absence from a later export does not establish when or why a message was removed. Please address any retained personal data associated with these IDs.\n\nScope: %s. Review this draft before sending.\n\n", owner, scope)
	if e != nil {
		return 0, e
	}
	c := csv.NewWriter(file)
	c.Comma = '\t'
	if e = c.Write([]string{"server_id", "server_name", "channel_id", "channel_name", "message_id"}); e != nil {
		return 0, e
	}
	count := 0
	e = s.Store.Each(ctx, f, func(r store.Row) error {
		if r.Guild == "" || r.Kind == "conflict" {
			return fmt.Errorf("unresolved server attribution")
		}
		count++
		return c.Write([]string{r.Guild, Safe(r.Server), r.Channel, Safe(r.Name), r.ID})
	})
	c.Flush()
	if e != nil {
		return 0, e
	}
	if e = c.Error(); e != nil {
		return 0, e
	}
	if count == 0 {
		return 0, fmt.Errorf("selection contains no resolved message IDs")
	}
	if e = file.Sync(); e != nil {
		return 0, e
	}
	if e = file.Close(); e != nil {
		return 0, e
	}
	success = true
	return count, nil
}
