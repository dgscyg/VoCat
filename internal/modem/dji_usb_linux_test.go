//go:build linux

package modem

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

func TestDJIUSBControlTransferLayout(t *testing.T) {
	var transfer usbControlTransfer
	if got := unsafe.Sizeof(transfer); got != 24 {
		t.Fatalf("usbControlTransfer size = %d, want 24", got)
	}
}

func TestDJISerialInterfaceLayout(t *testing.T) {
	if djiSerialFirst != 0 || djiSerialLast != 3 || djiATInterface != 2 || djiQMIInterface != 4 {
		t.Fatalf(
			"DJI interface layout = serial %d-%d, AT %d, QMI %d; want serial 0-3, AT 2, QMI 4",
			djiSerialFirst,
			djiSerialLast,
			djiATInterface,
			djiQMIInterface,
		)
	}
}

func TestEnsureDJIUSBCompositionNoDevice(t *testing.T) {
	root := t.TempDir()
	statuses, err := EnsureDJIUSBComposition(context.Background(), filepath.Join(root, "sys"), filepath.Join(root, "dev"))
	if err != nil {
		t.Fatalf("EnsureDJIUSBComposition() error = %v", err)
	}
	if len(statuses) != 0 {
		t.Fatalf("statuses = %#v, want none", statuses)
	}
}

func TestEnsureDJIUSBCompositionAlreadyCorrect(t *testing.T) {
	sysRoot, devRoot := setupDJIUSBTree(t, true)
	statuses, err := EnsureDJIUSBComposition(context.Background(), sysRoot, devRoot)
	if err != nil {
		t.Fatalf("EnsureDJIUSBComposition() error = %v", err)
	}
	if len(statuses) != 1 || statuses[0].Changed || statuses[0].ATDevice != filepath.Join(devRoot, "ttyUSB6") {
		t.Fatalf("statuses = %#v", statuses)
	}
	if statuses[0].ControlDevice != filepath.Join(devRoot, "cdc-wdm1") || statuses[0].NetworkInterface != "wwan1" {
		t.Fatalf("QMI status = %#v", statuses[0])
	}
}

func TestEnsureDJIUSBCompositionReclaimsQMIFromOption(t *testing.T) {
	sysRoot, devRoot := setupDJIUSBTree(t, false)
	sysfsControlWriter = func(path, value string) error {
		if err := writeSysfsFile(path, value); err != nil {
			return err
		}
		simulateUSBBind(path, value)
		return nil
	}
	t.Cleanup(func() { sysfsControlWriter = writeSysfsFile })

	statuses, err := EnsureDJIUSBComposition(context.Background(), sysRoot, devRoot)
	if err != nil {
		t.Fatalf("EnsureDJIUSBComposition() error = %v", err)
	}
	if len(statuses) != 1 || !statuses[0].Changed {
		t.Fatalf("statuses = %#v, want a changed DJI composition", statuses)
	}
	if statuses[0].ControlDevice != filepath.Join(devRoot, "cdc-wdm1") {
		t.Fatalf("control = %q, want cdc-wdm1 after reclaiming if4 from option", statuses[0].ControlDevice)
	}
	if statuses[0].NetworkInterface != "wwan1" {
		t.Fatalf("network = %q, want wwan1", statuses[0].NetworkInterface)
	}
	if got := usbInterfaceDriver(filepath.Join(sysRoot, "bus", "usb", "devices", "1-2:1.4")); got != "qmi_wwan" {
		t.Fatalf("if4 driver = %q, want qmi_wwan", got)
	}
	for index := 0; index <= 3; index++ {
		path := filepath.Join(sysRoot, "bus", "usb", "devices", "1-2:1."+strconv.Itoa(index))
		if got := usbInterfaceDriver(path); got != "option" {
			t.Fatalf("if%d driver = %q, want option", index, got)
		}
	}
}

func TestWriteSysfsDoesNotCreateMissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing")
	if err := writeSysfs(path, "value"); err == nil {
		t.Fatal("writeSysfs(missing) unexpectedly succeeded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing sysfs path was created: %v", err)
	}
}

func TestReadUSBNumber(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "busnum")
	if err := os.WriteFile(path, []byte("12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readUSBNumber(path); err != nil || got != 12 {
		t.Fatalf("readUSBNumber() = %d, %v, want 12, nil", got, err)
	}
	if err := os.WriteFile(path, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readUSBNumber(path); err == nil {
		t.Fatal("readUSBNumber(0) unexpectedly succeeded")
	}
}

func setupDJIUSBTree(t *testing.T, qmiBound bool) (string, string) {
	t.Helper()
	root := t.TempDir()
	sysRoot := filepath.Join(root, "sys")
	devRoot := filepath.Join(root, "dev")
	usbRoot := filepath.Join(sysRoot, "bus", "usb", "devices")
	driversRoot := filepath.Join(sysRoot, "bus", "usb", "drivers")
	optionRoot := filepath.Join(driversRoot, "option")
	qmiRoot := filepath.Join(driversRoot, "qmi_wwan")
	optionSerial := filepath.Join(sysRoot, "bus", "usb-serial", "drivers", "option1")
	mustMkdirAll(t, optionRoot)
	mustMkdirAll(t, qmiRoot)
	mustMkdirAll(t, optionSerial)
	for _, name := range []string{"bind", "unbind"} {
		mustWriteEmpty(t, filepath.Join(optionRoot, name))
		mustWriteEmpty(t, filepath.Join(qmiRoot, name))
	}
	mustWriteEmpty(t, filepath.Join(qmiRoot, "new_id"))
	mustWriteEmpty(t, filepath.Join(qmiRoot, "remove_id"))
	mustWriteEmpty(t, filepath.Join(optionSerial, "new_id"))

	devicePath := filepath.Join(usbRoot, "1-2")
	mustWrite(t, filepath.Join(devicePath, "idVendor"), "2ca3\n")
	mustWrite(t, filepath.Join(devicePath, "idProduct"), "4006\n")
	mustWrite(t, filepath.Join(devicePath, "busnum"), "1\n")
	mustWrite(t, filepath.Join(devicePath, "devnum"), "5\n")

	for index := 0; index <= 3; index++ {
		iface := filepath.Join(usbRoot, "1-2:1."+strconv.Itoa(index))
		mustMkdirAll(t, filepath.Join(iface, "ttyUSB"+strconv.Itoa(index+4)))
		if err := os.Symlink(optionRoot, filepath.Join(iface, "driver")); err != nil {
			t.Fatal(err)
		}
	}
	qmiIface := filepath.Join(usbRoot, "1-2:1.4")
	mustMkdirAll(t, qmiIface)
	if qmiBound {
		mustMkdirAll(t, filepath.Join(qmiIface, "usbmisc", "cdc-wdm1"))
		mustMkdirAll(t, filepath.Join(qmiIface, "net", "wwan1"))
		if err := os.Symlink(qmiRoot, filepath.Join(qmiIface, "driver")); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Symlink(optionRoot, filepath.Join(qmiIface, "driver")); err != nil {
		t.Fatal(err)
	}
	return sysRoot, devRoot
}

func simulateUSBBind(path, value string) {
	base := filepath.Base(path)
	driverDir := filepath.Dir(path)
	driverName := filepath.Base(driverDir)
	usbRoot := filepath.Join(filepath.Dir(filepath.Dir(driverDir)), "devices")
	ifacePath := filepath.Join(usbRoot, strings.TrimSpace(value))
	switch base {
	case "unbind":
		_ = os.Remove(filepath.Join(ifacePath, "driver"))
		_ = os.RemoveAll(filepath.Join(ifacePath, "usbmisc"))
		_ = os.RemoveAll(filepath.Join(ifacePath, "net"))
	case "bind":
		_ = os.Remove(filepath.Join(ifacePath, "driver"))
		_ = os.Symlink(driverDir, filepath.Join(ifacePath, "driver"))
		if driverName == "qmi_wwan" {
			_ = os.MkdirAll(filepath.Join(ifacePath, "usbmisc", "cdc-wdm1"), 0o755)
			_ = os.MkdirAll(filepath.Join(ifacePath, "net", "wwan1"), 0o755)
		}
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteEmpty(t *testing.T, path string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}
