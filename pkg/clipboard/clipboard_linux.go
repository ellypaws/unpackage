//go:build linux

package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

type tool struct {
	name      string
	args      []string
	available func() bool
}

var tools = []tool{
	{"wl-paste", []string{"--no-newline"}, func() bool { return os.Getenv("WAYLAND_DISPLAY") != "" }},
	{"xclip", []string{"-out", "-selection", "clipboard"}, func() bool { return os.Getenv("DISPLAY") != "" }},
	{"xsel", []string{"--output", "--clipboard"}, func() bool { return os.Getenv("DISPLAY") != "" }},
	{"powershell.exe", []string{"-NoProfile", "-Command", "Get-Clipboard", "-Raw"}, nil},
	{"termux-clipboard-get", nil, nil},
}

// Read tries each installed clipboard tool in order and returns the first
// non-empty text. Tools are tried at call time, so a tool that fails for one
// clipboard state does not block the others.
func Read(ctx context.Context) (string, error) {
	var failures []string
	found := false
	for _, t := range tools {
		if t.available != nil && !t.available() {
			continue
		}
		path, err := exec.LookPath(t.name)
		if err != nil {
			continue
		}
		found = true
		out, err := exec.CommandContext(ctx, path, t.args...).Output()
		if err != nil {
			failures = append(failures, t.name+": "+describe(err))
			continue
		}
		text := strings.TrimSuffix(string(out), "\r\n")
		if strings.TrimSpace(text) == "" {
			failures = append(failures, t.name+": clipboard is empty")
			continue
		}
		return text, nil
	}
	if !found {
		return "", errors.New("no clipboard tool found; install wl-clipboard, xclip, or xsel")
	}
	return "", fmt.Errorf("%s", strings.Join(failures, "; "))
}

func describe(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if msg := strings.TrimSpace(string(exit.Stderr)); msg != "" {
			return msg
		}
		return err.Error()
	}
	if errors.Is(err, syscall.ENOEXEC) {
		return "WSL interop is disabled, so Windows programs cannot run"
	}
	return err.Error()
}
