//go:build !linux

package modem

import "context"

// DJIUSBStatus is the host binding for one factory-ID DJI/Baiwang module.
type DJIUSBStatus struct {
	USBName          string
	USBDevice        string
	QMIInterface     string
	ATDevice         string
	ControlDevice    string
	NetworkInterface string
	Changed          bool
}

func EnsureDJIUSBComposition(context.Context, string, string) ([]DJIUSBStatus, error) {
	return nil, nil
}

func ensureDJIUSBComposition(context.Context, string, string) error {
	return nil
}
