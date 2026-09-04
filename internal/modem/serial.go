package modem

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type SerialOpener struct {
	SessionOptions SessionOptions
	BaudRate       int
}

func (opener SerialOpener) Open(ctx context.Context, port Port) (Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := port.OpenPath()
	if path == "" {
		return nil, errors.New("modem: candidate has no AT port")
	}
	if isNativeWWANATPath(path) {
		rawPort, err := openNativeWWANATTransport(path)
		if err != nil {
			return nil, fmt.Errorf("open WWAN AT port %s: %w", path, err)
		}
		if err := rawPort.ResetInputBuffer(); err != nil {
			_ = rawPort.Close()
			return nil, fmt.Errorf("reset WWAN AT input buffer %s: %w", path, err)
		}
		session, err := NewSession(rawPort, opener.SessionOptions)
		if err != nil {
			_ = rawPort.Close()
			return nil, err
		}
		return session, nil
	}
	baudRate := opener.BaudRate
	if baudRate <= 0 {
		baudRate = 115200
	}
	rawPort, err := openSerialTransport(path, baudRate)
	if err != nil {
		return nil, fmt.Errorf("open AT port %s: %w", path, err)
	}
	if err := rawPort.ResetInputBuffer(); err != nil {
		_ = rawPort.Close()
		return nil, fmt.Errorf("reset AT input buffer %s: %w", path, err)
	}
	session, err := NewSession(rawPort, opener.SessionOptions)
	if err != nil {
		_ = rawPort.Close()
		return nil, err
	}
	return session, nil
}

// OpenRawPort opens a USB serial (or WWAN AT) node for non-AT binary I/O such
// as Quectel QPCMV PCM on the NMEA interface.
func OpenRawPort(path string, baudRate int) (Transport, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return nil, errors.New("modem: raw port path is required")
	}
	if isNativeWWANATPath(path) {
		return openNativeWWANATTransport(path)
	}
	if baudRate <= 0 {
		baudRate = 115200
	}
	return openSerialTransport(path, baudRate)
}

func isNativeWWANATPath(path string) bool {
	_, kind, _, ok := parseWWANPortName(filepath.Base(filepath.Clean(path)))
	return ok && kind == "at"
}
