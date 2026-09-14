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

// RangeDays maps a statistics range key to a day count, zero meaning unbounded.
func RangeDays(key string) int {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "", "all", "0":
		return 0
	case "year":
		return 365
	}
	n, err := strconv.Atoi(strings.TrimSpace(key))
	if err != nil || n < 1 {
		return 0
	}
	return min(n, 100000)
}

// RangeWindow covers the last N local days including today.
func RangeWindow(days int, today time.Time) (time.Time, time.Time) {
	today = today.In(time.Local)
	start := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.Local)
	return start.AddDate(0, 0, 1-days), start.AddDate(0, 0, 1)
}

func Split(line string) ([]string, error) {
	if query, ok := strings.CutPrefix(strings.TrimSpace(line), "search"); ok && len(query) > 0 && (query[0] == ' ' || query[0] == '\t') {
		return []string{"search", strings.TrimSpace(query)}, nil
	}
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

const maxDaysAgo = 100000

// Dates parses date box input into calendar days or exact unix seconds.
// Whole numbers above maxDaysAgo are unix seconds; a fraction is dropped.
// Seconds map to message id 0 because typed times have no source message.
func Dates(text string, today time.Time) ([]string, map[int64]int64, error) {
	var dates []string
	var seconds map[int64]int64
	for group := range strings.SplitSeq(text, ";") {
		entries := []string{group}
		if _, _, err := dateEntry(group, today); err != nil && strings.Contains(group, ",") {
			entries = strings.Split(group, ",")
		}
		for _, entry := range entries {
			date, second, err := dateEntry(entry, today)
			if err != nil {
				return nil, nil, err
			}
			if second > 0 {
				if seconds == nil {
					seconds = map[int64]int64{}
				}
				seconds[second] = 0
				continue
			}
			dates = append(dates, date)
		}
	}
	if len(dates) > 0 && len(seconds) > 0 {
		return nil, nil, fmt.Errorf("use dates or unix times, not both")
	}
	return dates, seconds, nil
}

func dateEntry(text string, today time.Time) (string, int64, error) {
	text = strings.TrimSpace(text)
	if _, err := time.Parse(time.DateOnly, text); err == nil {
		return text, 0, nil
	}
	if strings.Contains(text, "-") {
		return "", 0, fmt.Errorf("invalid date; use YYYY-MM-DD")
	}
	if second, ok := unixEntry(text); ok {
		return "", second, nil
	}

	value := strings.ToLower(text)
	value = strings.TrimSpace(strings.TrimSuffix(value, "ago"))
	if withoutDays, ok := strings.CutSuffix(value, "days"); ok {
		value = strings.TrimSpace(withoutDays)
	} else if withoutDay, ok := strings.CutSuffix(value, "day"); ok {
		value = strings.TrimSpace(withoutDay)
	}
	if value == "" {
		return "", 0, fmt.Errorf("enter a date, days ago, or unix time")
	}
	groups := strings.Split(value, ",")
	if len(groups) > 1 {
		if len(groups[0]) < 1 || len(groups[0]) > 3 {
			return "", 0, fmt.Errorf("enter a date, days ago, or unix time")
		}
		for _, group := range groups {
			if group != strings.TrimSpace(group) || strings.Trim(group, "0123456789") != "" {
				return "", 0, fmt.Errorf("enter a date, days ago, or unix time")
			}
		}
		for _, group := range groups[1:] {
			if len(group) != 3 {
				return "", 0, fmt.Errorf("enter a date, days ago, or unix time")
			}
		}
	}
	value = strings.ReplaceAll(value, ",", "")
	if strings.Trim(value, "0123456789") != "" {
		return "", 0, fmt.Errorf("enter a date, days ago, or unix time")
	}
	n, err := strconv.Atoi(value)
	if err != nil || n > maxDaysAgo {
		return "", 0, fmt.Errorf("days must be between 0 and %d", maxDaysAgo)
	}
	return today.AddDate(0, 0, -n).Format(time.DateOnly), 0, nil
}

func unixEntry(text string) (int64, bool) {
	whole, fraction, _ := strings.Cut(text, ".")
	if whole == "" || strings.Trim(whole, "0123456789") != "" || strings.Trim(fraction, "0123456789") != "" {
		return 0, false
	}
	second, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || second <= maxDaysAgo {
		return 0, false
	}
	return second, true
}
