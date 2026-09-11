package logging

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Log struct {
	Logger    *slog.Logger
	mu        sync.Mutex
	lines     []string
	queue     chan []byte
	pipe      *io.PipeWriter
	done      chan struct{}
	fileDone  chan struct{}
	fileQueue chan string
	path      string
}

func New(dir string) *Log {
	l := &Log{queue: make(chan []byte, 256), done: make(chan struct{}), fileDone: make(chan struct{}), fileQueue: make(chan string, 256)}
	r, w := io.Pipe()
	l.pipe = w
	var f *os.File
	var failure string
	for i := range 3 {
		name := "log.txt"
		if i > 0 {
			name = fmt.Sprintf("log-%s-%d.txt", time.Now().Format("20060102-150405.000000000"), i)
		}
		p := filepath.Join(dir, name)
		var e error
		f, e = openFile(p)
		if e == nil {
			l.path = p
			break
		}
		if errors.Is(e, os.ErrPermission) {
			failure = "Log file permission denied; using the log tab"
			break
		}
	}
	if f == nil && failure == "" {
		failure = "Log file unavailable; using the log tab"
	}
	go func() {
		defer close(l.fileDone)
		if f != nil {
			defer f.Close()
		}
		for line := range l.fileQueue {
			if f != nil {
				if _, e := io.WriteString(f, line+"\n"); e != nil {
					l.append("Log file write failed; using the log tab")
					f.Close()
					f = nil
				}
			}
		}
	}()
	go func() {
		defer close(l.done)
		defer r.Close()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 4096), 65536)
		for sc.Scan() {
			line := sc.Text()
			l.append(line)
			select {
			case l.fileQueue <- line:
			default:
				l.append("File log queue full; entry retained in log tab")
			}
		}
		close(l.fileQueue)
	}()
	go func() {
		for b := range l.queue {
			if _, e := w.Write(b); e != nil {
				break
			}
		}
		w.Close()
	}()
	l.Logger = slog.New(slog.NewTextHandler(l, nil))
	if failure != "" {
		l.append(failure)
	}
	l.Logger.Info("Session started")
	return l
}
func (l *Log) Write(b []byte) (int, error) {
	v := append([]byte(nil), b...)
	select {
	case l.queue <- v:
	default:
		l.append("Log queue full; entry dropped")
	}
	return len(b), nil
}
func (l *Log) append(v string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, v)
	if len(l.lines) > 2000 {
		copy(l.lines, l.lines[len(l.lines)-2000:])
		l.lines = l.lines[:2000]
	}
}
func (l *Log) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}
func (l *Log) Close() {
	close(l.queue)
	<-l.done
	select {
	case <-l.fileDone:
	case <-time.After(time.Second):
	}
}
