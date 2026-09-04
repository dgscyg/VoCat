//go:build linux

package hostif

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestLookupMissingInterface(t *testing.T) {
	if _, err := Lookup("wwan-missing-vocat"); err == nil {
		t.Fatal("Lookup(missing) unexpectedly succeeded")
	}
	if Present("wwan-missing-vocat") {
		t.Fatal("Present(missing) = true")
	}
}

func TestLookupRejectsPathComponents(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../wwan0", "wwan0/evil", "interface-name-too-long"} {
		if _, err := Lookup(name); err == nil {
			t.Fatalf("Lookup(%q) unexpectedly succeeded", name)
		}
	}
}

func TestLookupSysfsFallback(t *testing.T) {
	root := t.TempDir()
	ifaceDir := filepath.Join(root, "wwan1")
	if err := os.MkdirAll(ifaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ifaceDir, "ifindex"), []byte("42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// IFF_UP|IFF_POINTOPOINT|IFF_NOARP|IFF_MULTICAST — qmi_wwan Raw-IP.
	if err := os.WriteFile(filepath.Join(ifaceDir, "flags"), []byte("0x1091\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	iface, err := lookupSysfs("wwan1", root)
	if err != nil {
		t.Fatalf("lookupSysfs() error = %v", err)
	}
	if iface.Index != 42 || iface.Name != "wwan1" {
		t.Fatalf("interface = %#v", iface)
	}
	if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagPointToPoint == 0 {
		t.Fatalf("flags = %v, want UP|POINTOPOINT from 0x1091", iface.Flags)
	}
}

func TestLookupLoopback(t *testing.T) {
	iface, err := Lookup("lo")
	if err != nil {
		t.Fatalf("Lookup(lo) error = %v", err)
	}
	if iface.Name != "lo" || iface.Flags&net.FlagUp == 0 {
		t.Fatalf("lo = %#v", iface)
	}
}
