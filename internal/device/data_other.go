//go:build !linux

package device

import (
	"context"
	"fmt"

	"vocat/internal/modem"
)

func setQMINetwork(
	context.Context,
	modem.Candidate,
	bool,
	string,
	string,
	string,
	string,
	string,
	modem.Client,
) (NetworkResult, error) {
	return NetworkResult{}, fmt.Errorf("%w: QMI control is supported only on Linux", ErrDataBackendUnavailable)
}

func qmiWWANRawIP(string) bool { return false }

func activateExportProxyInterface(context.Context, modem.Candidate, modem.Client) (string, error) {
	return "", nil
}

func deactivateExportProxyInterface(context.Context, string) {}
