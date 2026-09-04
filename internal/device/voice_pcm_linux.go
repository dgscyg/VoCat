//go:build linux

package device

import (
	"context"
	"fmt"

	"vocat/internal/modem"
)

func (manager *Manager) PrepareCallAudio(ctx context.Context, id string) error {
	state, err := manager.lookup(id)
	if err != nil {
		return err
	}
	state.opMu.Lock()
	defer state.opMu.Unlock()
	if err := manager.validateActive(id, state); err != nil {
		return err
	}
	if state.callAudio != nil && state.callAudio.transport != nil {
		return nil
	}
	if state.callAudioSkipped {
		return ErrCallAudioUnsupported
	}
	port := nmeaPort(manager.candidateFor(state))
	if port.OpenPath() == "" {
		state.callAudioSkipped = true
		return ErrCallAudioUnsupported
	}
	client, err := manager.clientLocked(ctx, state, manager.candidateFor(state))
	if err != nil {
		return err
	}
	if _, err := manager.command(ctx, client, "AT+QPCMV=?"); err != nil {
		state.callAudioSkipped = true
		return fmt.Errorf("%w: %v", ErrCallAudioUnsupported, err)
	}
	_, _ = manager.command(ctx, client, `AT+QGPSCFG="outport","none"`)
	if _, err := manager.command(ctx, client, "AT+QPCMV=1,0"); err != nil {
		if _, retryErr := manager.command(ctx, client, "AT+QPCMV=1"); retryErr != nil {
			state.callAudioSkipped = true
			return fmt.Errorf("%w: %v", ErrCallAudioUnsupported, err)
		}
	}
	transport, err := modem.OpenRawPort(port.OpenPath(), 115200)
	if err != nil {
		state.callAudioSkipped = true
		return fmt.Errorf("open USB call PCM port %s: %w", port.OpenPath(), err)
	}
	if err := transport.ResetInputBuffer(); err != nil {
		_ = transport.Close()
		return err
	}
	state.callAudio = &nmeaCallAudio{transport: transport}
	if manager.logger != nil {
		manager.logger.Info("enabled USB call PCM", "device_id", id, "port", port.OpenPath())
	}
	return nil
}
