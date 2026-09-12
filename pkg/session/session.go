package session

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ellypaws/unpackage/pkg/importer"
	"github.com/ellypaws/unpackage/pkg/logging"
	"github.com/ellypaws/unpackage/pkg/store"
)

type Session struct {
	Store  *store.Store
	Log    *logging.Log
	mu     sync.Mutex
	jobs   map[int]*importJob
	wg     sync.WaitGroup
	files  chan struct{}
	Filter store.Filter
	Today  time.Time
}
type importJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func New() (*Session, error) {
	parallelism := min(64, max(8, runtime.GOMAXPROCS(0)*2))
	return &Session{Store: store.New(), Log: logging.New("."), jobs: map[int]*importJob{}, files: make(chan struct{}, parallelism), Filter: store.Filter{Mode: "auto", Limit: 50}, Today: time.Now()}, nil
}
func (s *Session) Close() { s.Stop(); s.wg.Wait(); s.Log.Close() }
func (s *Session) Open(parent context.Context, slot int, p string) error {
	if slot < 0 || slot > 1 {
		return fmt.Errorf("slot must be older or newer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[slot]; ok {
		return fmt.Errorf("slot is loading; stop and wait before replacing it")
	}
	if e := s.Store.Reset(parent, slot); e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(parent)
	job := &importJob{cancel: cancel, done: make(chan struct{})}
	s.jobs[slot] = job
	s.wg.Go(func() {
		defer cancel()
		e := importer.Load(ctx, s.Store, slot, p, s.files, s.Log.Logger)
		if e != nil {
			s.Log.Logger.Warn("Import retained partial data", "slot", slot)
		}
		s.mu.Lock()
		delete(s.jobs, slot)
		close(job.done)
		s.mu.Unlock()
	})
	return nil
}
func (s *Session) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.jobs {
		c.cancel()
	}
}
func (s *Session) Clear(ctx context.Context, slot int) error {
	if slot < 0 || slot > 1 {
		return fmt.Errorf("choose older or newer")
	}
	s.mu.Lock()
	job := s.jobs[slot]
	if job != nil {
		job.cancel()
	}
	s.mu.Unlock()
	if job != nil {
		select {
		case <-job.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs[slot] != nil {
		return fmt.Errorf("another import has started")
	}
	s.Store.Clear(slot)
	return nil
}
func (s *Session) Busy() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.jobs) > 0 }
func (s *Session) Wait(ctx context.Context) error {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for s.Busy() {
		select {
		case <-ctx.Done():
			s.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

func Split(line string) ([]string, error) {
	var out []string
	var b strings.Builder
	var quote rune
	started := false
	for _, r := range strings.TrimSpace(line) {
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			started = true
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
			continue
		}
		if r == ' ' || r == '\t' {
			if started {
				out = append(out, b.String())
				b.Reset()
				started = false
			}
		} else {
			b.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote")
	}
	if started {
		out = append(out, b.String())
	}
	return out, nil
}
func Dates(text string, today time.Time) ([]string, error) {
	var out []string
	for group := range strings.SplitSeq(text, ";") {
		entries := []string{group}
		if _, err := dateEntry(group, today); err != nil && strings.Contains(group, ",") {
			entries = strings.Split(group, ",")
		}
		for _, entry := range entries {
			date, err := dateEntry(entry, today)
			if err != nil {
				return nil, err
			}
			out = append(out, date)
		}
	}
	return out, nil
}

func dateEntry(text string, today time.Time) (string, error) {
	text = strings.TrimSpace(text)
	if _, err := time.Parse(time.DateOnly, text); err == nil {
		return text, nil
	}
	if strings.Contains(text, "-") {
		return "", fmt.Errorf("invalid date; use YYYY-MM-DD")
	}

	value := strings.ToLower(text)
	value = strings.TrimSpace(strings.TrimSuffix(value, "ago"))
	if withoutDays, ok := strings.CutSuffix(value, "days"); ok {
		value = strings.TrimSpace(withoutDays)
	} else if withoutDay, ok := strings.CutSuffix(value, "day"); ok {
		value = strings.TrimSpace(withoutDay)
	}
	if value == "" {
		return "", fmt.Errorf("enter a date or days ago")
	}
	groups := strings.Split(value, ",")
	if len(groups) > 1 {
		if len(groups[0]) < 1 || len(groups[0]) > 3 {
			return "", fmt.Errorf("enter a date or days ago")
		}
		for _, group := range groups {
			if group != strings.TrimSpace(group) || strings.Trim(group, "0123456789") != "" {
				return "", fmt.Errorf("enter a date or days ago")
			}
		}
		for _, group := range groups[1:] {
			if len(group) != 3 {
				return "", fmt.Errorf("enter a date or days ago")
			}
		}
	}
	value = strings.ReplaceAll(value, ",", "")
	if strings.Trim(value, "0123456789") != "" {
		return "", fmt.Errorf("enter a date or days ago")
	}
	n, err := strconv.Atoi(value)
	if err != nil || n > 100000 {
		return "", fmt.Errorf("days must be between 0 and 100000")
	}
	return today.AddDate(0, 0, -n).Format(time.DateOnly), nil
}
