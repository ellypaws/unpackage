//go:build !linux

package clipboard

import (
	"context"

	"github.com/atotto/clipboard"
)

// Read returns the clipboard text through the platform API.
func Read(context.Context) (string, error) {
	return clipboard.ReadAll()
}
