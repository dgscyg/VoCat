package exportproxy

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
)

func encodeDNSQueryA(name string) ([]byte, uint16, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" {
		return nil, 0, errors.New("empty dns name")
	}
	var id uint16
	if err := binary.Read(rand.Reader, binary.BigEndian, &id); err != nil {
		return nil, 0, err
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, id)
	_ = binary.Write(&buf, binary.BigEndian, uint16(0x0100))
	_ = binary.Write(&buf, binary.BigEndian, uint16(1))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 {
			return nil, 0, fmt.Errorf("invalid dns label %q", label)
		}
		buf.WriteByte(byte(len(label)))
		buf.WriteString(label)
	}
	buf.WriteByte(0)
	_ = binary.Write(&buf, binary.BigEndian, uint16(1))
	_ = binary.Write(&buf, binary.BigEndian, uint16(1))
	return buf.Bytes(), id, nil
}

func parseDNSResponseA(payload []byte, id uint16, name string) ([]net.IP, error) {
	if len(payload) < 12 {
		return nil, errors.New("dns response is too short")
	}
	if binary.BigEndian.Uint16(payload[0:2]) != id {
		return nil, errors.New("dns response id mismatch")
	}
	flags := binary.BigEndian.Uint16(payload[2:4])
	if flags&0x8000 == 0 {
		return nil, errors.New("dns payload is not a response")
	}
	if rcode := flags & 0x000f; rcode != 0 {
		return nil, fmt.Errorf("dns rcode %d", rcode)
	}
	qd := int(binary.BigEndian.Uint16(payload[4:6]))
	an := int(binary.BigEndian.Uint16(payload[6:8]))
	ns := int(binary.BigEndian.Uint16(payload[8:10]))
	ar := int(binary.BigEndian.Uint16(payload[10:12]))
	offset := 12
	for i := 0; i < qd; i++ {
		next, _, err := decodeDNSName(payload, offset)
		if err != nil {
			return nil, err
		}
		offset = next + 4
		if offset > len(payload) {
			return nil, errors.New("dns question is truncated")
		}
	}
	ips, offset, err := collectDNSARecords(payload, offset, an)
	if err != nil {
		return nil, err
	}
	offset, err = skipDNSRecords(payload, offset, ns)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		extra, _, extraErr := collectDNSARecords(payload, offset, ar)
		if extraErr != nil {
			return nil, extraErr
		}
		ips = extra
	}
	if len(ips) == 0 {
		if flags&0x0200 != 0 {
			return nil, errDNSTruncated
		}
		return nil, fmt.Errorf("no A records for %s", name)
	}
	return ips, nil
}

var errDNSTruncated = errors.New("dns response is truncated")

func collectDNSARecords(payload []byte, offset, count int) ([]net.IP, int, error) {
	var ips []net.IP
	for i := 0; i < count; i++ {
		next, _, err := decodeDNSName(payload, offset)
		if err != nil {
			return nil, offset, err
		}
		if next+10 > len(payload) {
			return nil, offset, errors.New("dns record is truncated")
		}
		qtype := binary.BigEndian.Uint16(payload[next : next+2])
		dataLen := int(binary.BigEndian.Uint16(payload[next+8 : next+10]))
		dataStart := next + 10
		dataEnd := dataStart + dataLen
		if dataEnd > len(payload) {
			return nil, offset, errors.New("dns record data is truncated")
		}
		if qtype == 1 && dataLen == 4 {
			ip := net.IPv4(payload[dataStart], payload[dataStart+1], payload[dataStart+2], payload[dataStart+3]).To4()
			if ip != nil {
				ips = append(ips, ip)
			}
		}
		offset = dataEnd
	}
	return ips, offset, nil
}

func skipDNSRecords(payload []byte, offset, count int) (int, error) {
	_, next, err := collectDNSARecords(payload, offset, count)
	return next, err
}

func decodeDNSName(payload []byte, offset int) (int, string, error) {
	if offset >= len(payload) {
		return 0, "", errors.New("dns name is truncated")
	}
	var labels []string
	cursor := offset
	jumped := false
	end := offset
	for hops := 0; hops < 16; hops++ {
		if cursor >= len(payload) {
			return 0, "", errors.New("dns name is truncated")
		}
		length := int(payload[cursor])
		if length == 0 {
			if !jumped {
				end = cursor + 1
			}
			return end, strings.Join(labels, "."), nil
		}
		if length&0xc0 == 0xc0 {
			if cursor+1 >= len(payload) {
				return 0, "", errors.New("dns compression pointer is truncated")
			}
			pointer := int(binary.BigEndian.Uint16(payload[cursor:cursor+2]) & 0x3fff)
			if !jumped {
				end = cursor + 2
				jumped = true
			}
			cursor = pointer
			continue
		}
		if length&0xc0 != 0 {
			return 0, "", errors.New("invalid dns name length")
		}
		cursor++
		if cursor+length > len(payload) {
			return 0, "", errors.New("dns label is truncated")
		}
		labels = append(labels, strings.ToLower(string(payload[cursor:cursor+length])))
		cursor += length
		if !jumped {
			end = cursor
		}
	}
	return 0, "", errors.New("dns name pointer loop")
}
