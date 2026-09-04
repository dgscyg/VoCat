//go:build !linux

package device

import (
	"context"

	"vocat/internal/modem"
)

func qmiWWANRawIP(string) bool { return false }

func activateExportProxyInterface(context.Context, modem.Candidate, modem.Client) (string, error) {
	return "", nil
}

func deactivateExportProxyInterface(context.Context, string) {}
