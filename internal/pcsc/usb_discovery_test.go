package pcsc

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDiscoverUSBSmartCardReadersFindsACR38(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot represent Linux sysfs interface names containing a colon")
	}
	root := t.TempDir()
	usbRoot := filepath.Join(root, "bus", "usb", "devices")
	writeUSBTestFile(t, filepath.Join(usbRoot, "2-1", "idVendor"), "072f\n")
	writeUSBTestFile(t, filepath.Join(usbRoot, "2-1", "idProduct"), "90cc\n")
	writeUSBTestFile(t, filepath.Join(usbRoot, "2-1", "manufacturer"), "Advanced Card Systems\n")
	writeUSBTestFile(t, filepath.Join(usbRoot, "2-1", "product"), "ACR38 SmartCard Reader\n")
	writeUSBTestFile(t, filepath.Join(usbRoot, "2-1:1.0", "bInterfaceClass"), "0b\n")
	writeUSBTestFile(t, filepath.Join(usbRoot, "3-1", "idVendor"), "2c7c\n")
	writeUSBTestFile(t, filepath.Join(usbRoot, "3-1:1.0", "bInterfaceClass"), "ff\n")

	readers := discoverUSBSmartCardReaders(root, "pcsc_service_unavailable")
	if len(readers) != 1 {
		t.Fatalf("readers = %#v", readers)
	}
	reader := readers[0]
	if reader.USBPath != "2-1" || reader.VendorID != "072f" || reader.ProductID != "90cc" || reader.DiscoveryIssue != "pcsc_service_unavailable" {
		t.Fatalf("reader = %#v", reader)
	}
}

func TestSmartCardUSBDeviceName(t *testing.T) {
	if name, ok := smartCardUSBDeviceName("2-1:1.0", "0B"); !ok || name != "2-1" {
		t.Fatalf("smartCardUSBDeviceName() = %q, %v", name, ok)
	}
	if _, ok := smartCardUSBDeviceName("2-1:1.0", "ff"); ok {
		t.Fatal("vendor-specific interface was accepted as CCID")
	}
}

func TestMergePCSCAndSingleUSBReaderEnrichesFallbackPath(t *testing.T) {
	readers := mergePCSCAndUSBReaders(
		[]Reader{{Name: "ACS ACR38 00 00", USBPath: "pcsc:ACS ACR38 00 00", CardPresent: true}},
		[]Reader{{Name: "ACR38", USBPath: "2-1", VendorID: "072f", ProductID: "90cc", DiscoveryIssue: "pcsc_driver_missing"}},
	)
	if len(readers) != 1 || readers[0].USBPath != "2-1" || readers[0].DiscoveryIssue != "" || !readers[0].CardPresent {
		t.Fatalf("readers = %#v", readers)
	}
}

func TestMergePCSCAndMultipleUSBReadersWithFallbackPath(t *testing.T) {
	readers := mergePCSCAndUSBReaders(
		[]Reader{
			{Name: "Identiv uTrust 00 00", USBPath: "1-2", CardPresent: true},
			{Name: "Generic Smart Card Reader 00 00", USBPath: "pcsc:Generic Smart Card Reader 00 00", CardPresent: true},
		},
		[]Reader{
			{Name: "uTrust", USBPath: "1-2", VendorID: "04e6", ProductID: "5810", DiscoveryIssue: "pcsc_driver_missing"},
			{Name: "ESTKme-RED", USBPath: "1-1", VendorID: "0bda", ProductID: "0165", DiscoveryIssue: "pcsc_driver_missing"},
		},
	)
	if len(readers) != 2 {
		t.Fatalf("len(readers) = %d, want 2", len(readers))
	}
	for _, r := range readers {
		if r.DiscoveryIssue != "" {
			t.Errorf("reader %#v still has discovery issue %q", r, r.DiscoveryIssue)
		}
	}
}

func TestFilterVirtualPCDReadersKeepsPhysicalReaders(t *testing.T) {
	readers := filterVirtualPCDReaders([]Reader{
		{Name: "Virtual PCD 00 00", USBPath: "pcsc:Virtual PCD 00 00"},
		{Name: "Virtual PCD 00 01", USBPath: "pcsc:Virtual PCD 00 01"},
		{Name: "Generic Smart Card Reader 00 00", USBPath: "pcsc:Generic Smart Card Reader 00 00"},
		{Name: "Virtual PCD 00 02", USBPath: "2-1", VendorID: "1234", ProductID: "5678"},
	})
	if len(readers) != 2 {
		t.Fatalf("readers = %#v, want two real/unresolved non-virtual readers", readers)
	}
	if readers[0].Name != "Generic Smart Card Reader 00 00" || readers[1].USBPath != "2-1" {
		t.Fatalf("retained readers = %#v", readers)
	}
}

func writeUSBTestFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}
