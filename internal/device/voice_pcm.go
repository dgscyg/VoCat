package device

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"vocat/internal/modem"
	"vocat/internal/vowifi"
)

const (
	callPCMSampleRate = 8000
	callPCMFrameSamp  = 160
)

var ErrCallAudioUnsupported = errors.New("modem does not expose USB call PCM")

type nmeaCallAudio struct {
	mu        sync.Mutex
	transport modem.Transport
}

func (audio *nmeaCallAudio) Codec() string { return "L16/8000" }

func (audio *nmeaCallAudio) Close() error {
	if audio == nil {
		return nil
	}
	audio.mu.Lock()
	defer audio.mu.Unlock()
	if audio.transport == nil {
		return nil
	}
	err := audio.transport.Close()
	audio.transport = nil
	return err
}

func (audio *nmeaCallAudio) ReadPCM(ctx context.Context) ([]int16, error) {
	if audio == nil {
		return nil, ErrCallAudioUnsupported
	}
	buf := make([]byte, callPCMFrameSamp*2)
	offset := 0
	for offset < len(buf) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		audio.mu.Lock()
		transport := audio.transport
		audio.mu.Unlock()
		if transport == nil {
			return nil, io.EOF
		}
		count, err := transport.Read(buf[offset:])
		if count > 0 {
			offset += count
			continue
		}
		if err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	samples := make([]int16, callPCMFrameSamp)
	for index := range samples {
		samples[index] = int16(binary.LittleEndian.Uint16(buf[index*2:]))
	}
	return samples, nil
}

func (audio *nmeaCallAudio) WritePCM(samples []int16) error {
	if audio == nil {
		return ErrCallAudioUnsupported
	}
	if len(samples) == 0 {
		return nil
	}
	buf := make([]byte, len(samples)*2)
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(buf[index*2:], uint16(sample))
	}
	audio.mu.Lock()
	transport := audio.transport
	audio.mu.Unlock()
	if transport == nil {
		return io.EOF
	}
	_, err := transport.Write(buf)
	return err
}

func closeCallAudio(state *managedDevice) {
	if state == nil || state.callAudio == nil {
		return
	}
	_ = state.callAudio.Close()
	state.callAudio = nil
}

func nmeaPort(candidate modem.Candidate) modem.Port {
	for _, port := range candidate.Ports {
		if port.Role == modem.PortRoleNMEA && port.OpenPath() != "" {
			return port
		}
	}
	return modem.Port{}
}

func (manager *Manager) CallAudioReady(id string) bool {
	state, err := manager.lookup(id)
	if err != nil {
		return false
	}
	state.opMu.Lock()
	defer state.opMu.Unlock()
	return state.callAudio != nil && state.callAudio.transport != nil
}

func (manager *Manager) CallAudio(_ context.Context, id string) (vowifi.CallMedia, error) {
	state, err := manager.lookup(id)
	if err != nil {
		return nil, err
	}
	state.opMu.Lock()
	defer state.opMu.Unlock()
	if state.callAudio == nil || state.callAudio.transport == nil {
		return nil, ErrCallAudioUnsupported
	}
	return state.callAudio, nil
}
