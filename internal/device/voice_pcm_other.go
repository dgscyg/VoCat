//go:build !linux

package device

import "context"

func (manager *Manager) PrepareCallAudio(context.Context, string) error {
	return ErrCallAudioUnsupported
}
