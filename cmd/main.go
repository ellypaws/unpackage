package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/ellypaws/unpackage/pkg/session"
	"github.com/ellypaws/unpackage/pkg/tui"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "Error:", e)
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	a := os.Args[1:]
	if len(a) > 0 && (a[0] == "help" || a[0] == "--help" || a[0] == "-h") {
		fmt.Println(session.StyledHelp())
		return nil
	}
	s, e := session.New()
	if e != nil {
		return e
	}
	defer s.Close()
	if len(a) == 0 {
		if !term.IsTerminal(os.Stdin.Fd()) {
			return fmt.Errorf("TUI needs a terminal; use repl or diff for piped input")
		}
		m := tui.New(ctx, s)
		defer m.Zones.Close()
		_, e := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithReportFocus(), tea.WithContext(ctx)).Run()
		return e
	}
	if a[0] == "repl" {
		interactive := term.IsTerminal(os.Stdin.Fd())
		if interactive {
			fmt.Println(session.StyledHelp())
		}
		scan := bufio.NewScanner(os.Stdin)
		scan.Buffer(make([]byte, 4096), 65536)
		for {
			if interactive {
				fmt.Fprint(os.Stderr, "› ")
			}
			if !scan.Scan() {
				break
			}
			args, e := session.Split(scan.Text())
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				continue
			}
			if len(args) > 0 && (args[0] == "quit" || args[0] == "exit") {
				break
			}
			if e = s.Execute(ctx, args, os.Stdout); e != nil {
				fmt.Fprintln(os.Stderr, "Error:", e)
			}
		}
		return scan.Err()
	}
	if a[0] != "diff" {
		return s.Execute(ctx, a, os.Stdout)
	}
	if len(a) < 3 {
		return fmt.Errorf("diff requires OLDER and NEWER paths")
	}
	format := "jsonl"
	request := ""
	scope := "all"
	for i := 3; i < len(a); i++ {
		if i+1 >= len(a) {
			return fmt.Errorf("missing value for %s", a[i])
		}
		key, val := a[i], a[i+1]
		i++
		switch key {
		case "--format":
			format = val
		case "--request":
			request = val
		case "--scope":
			scope = val
		case "--server":
			s.Filter.Guilds = append(s.Filter.Guilds, strings.Split(val, ",")...)
		case "--date":
			ds, e := session.Dates(val, s.Today)
			if e != nil {
				return e
			}
			s.Filter.Dates = append(s.Filter.Dates, ds...)
		case "--search":
			s.Filter.Search = val
		case "--media":
			if val != "all" && val != "attachments" && val != "media" {
				return fmt.Errorf("media must be all, attachments or media")
			}
			s.Filter.Media = val
			if val == "all" {
				s.Filter.Media = ""
			}
		case "--before-days", "--after-days":
			n, e := session.Margin(val)
			if e != nil {
				return e
			}
			if key == "--before-days" {
				s.Filter.DateBefore = n
			} else {
				s.Filter.DateAfter = n
			}
		case "--mode":
			s.Filter.Mode = val
		default:
			return fmt.Errorf("unknown option %s", key)
		}
	}
	if e = s.Open(ctx, 0, a[1]); e != nil {
		return e
	}
	if e = s.Open(ctx, 1, a[2]); e != nil {
		return e
	}
	if e = s.Wait(ctx); e != nil {
		return e
	}
	if ok, why := s.Store.Compatible(ctx); !ok {
		return fmt.Errorf("%s", why)
	}
	ss, e := s.Store.Snapshots(ctx)
	if e != nil {
		return e
	}
	for _, v := range ss {
		if v.Errors > 0 {
			fmt.Fprintln(os.Stderr, "Warning: metadata import incomplete; inspect status in REPL")
		}
	}
	if request != "" {
		n, e := s.Request(ctx, request, scope)
		if e == nil {
			fmt.Fprintf(os.Stderr, "Saved draft with %d message IDs\n", n)
		}
		return e
	}
	s.Filter.Limit = 0
	return s.Output(ctx, s.Filter, format, os.Stdout)
}
