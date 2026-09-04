package ims

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"unicode"
)

func dtmfEventCode(digit rune) (byte, bool) {
	switch digit {
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return byte(digit - '0'), true
	case '*':
		return 10, true
	case '#':
		return 11, true
	case 'a', 'A':
		return 12, true
	case 'b', 'B':
		return 13, true
	case 'c', 'C':
		return 14, true
	case 'd', 'D':
		return 15, true
	default:
		return 0, false
	}
}

func normalizeDTMFDigits(value string) (string, error) {
	var out strings.Builder
	for _, digit := range value {
		if unicode.IsSpace(digit) {
			continue
		}
		if _, ok := dtmfEventCode(digit); !ok {
			return "", fmt.Errorf("ims: invalid DTMF digit %q", digit)
		}
		out.WriteRune(digit)
	}
	digits := out.String()
	if digits == "" {
		return "", errors.New("ims: DTMF digits are required")
	}
	if len(digits) > 32 {
		return "", errors.New("ims: DTMF sequence is too long")
	}
	return digits, nil
}

func (media *rtpMedia) SendDTMF(digits string) error {
	normalized, err := normalizeDTMFDigits(digits)
	if err != nil {
		return err
	}
	media.mu.RLock()
	var remote *net.UDPAddr
	if media.remote != nil {
		copy := *media.remote
		remote = &copy
	}
	dtmfPT := media.dtmfPT
	media.mu.RUnlock()
	if remote == nil {
		return errors.New("ims: RTP media is not negotiated")
	}
	if dtmfPT == 0 {
		dtmfPT = 100
	}
	media.writeMu.Lock()
	defer media.writeMu.Unlock()
	for _, digit := range normalized {
		code, _ := dtmfEventCode(digit)
		if err := media.writeRFC4733(remote, dtmfPT, code); err != nil {
			return err
		}
	}
	return nil
}

func (media *rtpMedia) writeRFC4733(remote *net.UDPAddr, payloadType, event byte) error {
	timestamp := media.timestamp
	const volume = 10
	durations := []uint16{160, 320, 480}
	for index, duration := range durations {
		if err := media.writeRFC4733Packet(remote, payloadType, event, volume, duration, index == 0, false); err != nil {
			return err
		}
	}
	for range 3 {
		if err := media.writeRFC4733Packet(remote, payloadType, event, volume, 480, false, true); err != nil {
			return err
		}
	}
	media.timestamp = timestamp + 800
	return nil
}

func (media *rtpMedia) writeRFC4733Packet(remote *net.UDPAddr, payloadType, event, volume byte, duration uint16, marker, end bool) error {
	packet := make([]byte, 16)
	packet[0] = 0x80
	packet[1] = payloadType
	if marker {
		packet[1] |= 0x80
	}
	binary.BigEndian.PutUint16(packet[2:4], media.sequence)
	binary.BigEndian.PutUint32(packet[4:8], media.timestamp)
	binary.BigEndian.PutUint32(packet[8:12], media.ssrc)
	packet[12] = event
	packet[13] = volume & 0x3f
	if end {
		packet[13] |= 0x80
	}
	binary.BigEndian.PutUint16(packet[14:16], duration)
	if _, err := media.conn.WriteToUDP(packet, remote); err != nil {
		return fmt.Errorf("ims: send DTMF: %w", err)
	}
	media.sequence++
	return nil
}
