//go:build linux

package hostif

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/sys/unix"
)

var classNetDir = "/sys/class/net"

// Lookup returns the named host interface. net.InterfaceByName walks a netlink
// dump that can skip qmi_wwan Raw-IP links (ARPHRD_NONE, empty MAC) and also
// races the brief unregister while qmi/raw_ip is rewritten. Sysfs ifindex is
// the kernel's own name-to-index table and is what `ip` uses.
func Lookup(name string) (*net.Interface, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return nil, fmt.Errorf("invalid interface name %q", name)
	}
	if iface, err := net.InterfaceByName(name); err == nil {
		return iface, nil
	}
	return lookupSysfs(name, classNetDir)
}

// Present reports whether the kernel currently has a netdev with this name.
func Present(name string) bool {
	_, err := Lookup(name)
	return err == nil
}

func lookupSysfs(name, classNet string) (*net.Interface, error) {
	indexRaw, err := os.ReadFile(filepath.Join(classNet, name, "ifindex"))
	if err != nil {
		return nil, err
	}
	index, err := strconv.Atoi(strings.TrimSpace(string(indexRaw)))
	if err != nil || index <= 0 {
		return nil, fmt.Errorf("invalid ifindex for %s", name)
	}
	return &net.Interface{
		Index:        index,
		Name:         name,
		Flags:        sysfsFlags(filepath.Join(classNet, name, "flags")),
		HardwareAddr: sysfsHardwareAddr(filepath.Join(classNet, name, "address")),
	}, nil
}

func sysfsFlags(path string) net.Flags {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	text := strings.TrimSpace(string(raw))
	value, err := strconv.ParseUint(strings.TrimPrefix(text, "0x"), 16, 32)
	if err != nil {
		value, err = strconv.ParseUint(text, 10, 32)
		if err != nil {
			return 0
		}
	}
	flags := net.Flags(0)
	if value&unix.IFF_UP != 0 {
		flags |= net.FlagUp
	}
	if value&unix.IFF_BROADCAST != 0 {
		flags |= net.FlagBroadcast
	}
	if value&unix.IFF_LOOPBACK != 0 {
		flags |= net.FlagLoopback
	}
	if value&unix.IFF_POINTOPOINT != 0 {
		flags |= net.FlagPointToPoint
	}
	if value&unix.IFF_MULTICAST != 0 {
		flags |= net.FlagMulticast
	}
	if value&unix.IFF_RUNNING != 0 {
		flags |= net.FlagRunning
	}
	return flags
}

func sysfsHardwareAddr(path string) net.HardwareAddr {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "00:00:00:00:00:00" {
		return nil
	}
	addr, err := net.ParseMAC(text)
	if err != nil {
		return nil
	}
	return addr
}

// Linux IFNAMSIZ is 16 including the terminator. Rejecting path punctuation
// keeps a stored device value from ever becoming a filesystem path component.
func validName(value string) bool {
	if value == "" || len(value) > 15 || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if character > unicode.MaxASCII || !(character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == '.') {
			return false
		}
	}
	return true
}
