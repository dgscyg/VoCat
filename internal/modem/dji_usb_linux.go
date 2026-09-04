//go:build linux

package modem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	djiSerialFirst   = 0
	djiSerialLast    = 3
	djiATInterface   = 2
	djiQMIInterface  = 4
	djiUSBID         = djiVendorID + " " + dji4GProductID
	djiNodeWait      = 2 * time.Second
	djiNodePollEvery = 25 * time.Millisecond
)

// DJIUSBStatus is the host binding for one factory-ID DJI/Baiwang 2ca3:4006
// module after EnsureDJIUSBComposition.
type DJIUSBStatus struct {
	USBName          string
	USBDevice        string
	QMIInterface     string
	ATDevice         string
	ControlDevice    string
	NetworkInterface string
	Changed          bool
}

type usbControlTransfer struct {
	RequestType uint8
	Request     uint8
	Value       uint16
	Index       uint16
	Length      uint16
	Timeout     uint32
	Data        uintptr
}

// EnsureDJIUSBComposition binds factory-ID DJI/Baiwang 2ca3:4006 serial
// interfaces 0-3 to option and interface 4 to qmi_wwan.
//
// option's dynamic ID is device-wide: once 2ca3:4006 is registered, a later USB
// reprobe lets option steal interface 4 and the QMI netdev (wwanN) disappears
// while AT still works. vocat then keeps dialing the stored name and reports
// "wwanN is not present on this host". Reclaiming if4 on every discovery is
// what keeps the host netdev alive without rewriting the module's USB identity.
func EnsureDJIUSBComposition(ctx context.Context, sysRoot, devRoot string) ([]DJIUSBStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sysRoot = filepath.Clean(sysRoot)
	devRoot = filepath.Clean(devRoot)
	usbRoot := filepath.Join(sysRoot, "bus", "usb", "devices")
	names, err := listDJIUSBNames(usbRoot)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}
	driversRoot := filepath.Join(sysRoot, "bus", "usb", "drivers")
	if err := ensureUSBDriverLoaded(ctx, driversRoot, "option", "option"); err != nil {
		return nil, err
	}
	if err := ensureUSBDriverLoaded(ctx, driversRoot, "qmi_wwan", "qmi_wwan"); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(sysRoot, "bus", "usb-serial", "drivers", "option1")); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect option USB-serial driver: %w", err)
	}

	qmiDriverRoot := filepath.Join(driversRoot, "qmi_wwan")
	if err := removeDynamicUSBID(qmiDriverRoot, djiUSBID); err != nil {
		return nil, fmt.Errorf("remove broad DJI qmi_wwan dynamic ID: %w", err)
	}

	result := make([]DJIUSBStatus, 0, len(names))
	var repairErrors []error
	for _, usbName := range names {
		status, repairErr := ensureOneDJIUSB(ctx, sysRoot, devRoot, usbRoot, driversRoot, usbName)
		if repairErr != nil {
			repairErrors = append(repairErrors, fmt.Errorf("%s: %w", usbName, repairErr))
			continue
		}
		result = append(result, status)
	}
	return result, errors.Join(repairErrors...)
}

func listDJIUSBNames(usbRoot string) ([]string, error) {
	entries, err := os.ReadDir(usbRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read USB topology: %w", err)
	}
	var names []string
	for _, entry := range entries {
		devicePath := filepath.Join(usbRoot, entry.Name())
		vendor, vendorErr := readTrimmedFile(filepath.Join(devicePath, "idVendor"))
		product, productErr := readTrimmedFile(filepath.Join(devicePath, "idProduct"))
		if vendorErr == nil && productErr == nil && IsDJI4GUSB(vendor, product) {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

func ensureOneDJIUSB(
	ctx context.Context,
	sysRoot, devRoot, usbRoot, driversRoot, usbName string,
) (DJIUSBStatus, error) {
	status := DJIUSBStatus{
		USBName:      usbName,
		QMIInterface: fmt.Sprintf("%s:1.%d", usbName, djiQMIInterface),
	}
	serialChanged, atDevice, err := bindDJISerialInterfaces(ctx, sysRoot, devRoot, usbRoot, driversRoot, usbName)
	if err != nil {
		return status, err
	}
	status.ATDevice = atDevice
	status.Changed = serialChanged

	qmiChanged, control, network, err := bindDJIQMIFunction(ctx, sysRoot, devRoot, usbRoot, driversRoot, usbName)
	if err != nil {
		return status, err
	}
	status.ControlDevice = control
	status.NetworkInterface = network
	status.Changed = status.Changed || qmiChanged
	if node, nodeErr := usbDeviceNode(filepath.Join(usbRoot, usbName), devRoot); nodeErr == nil {
		status.USBDevice = node
	}
	return status, nil
}

func bindDJISerialInterfaces(
	ctx context.Context,
	sysRoot, devRoot, usbRoot, driversRoot, usbName string,
) (bool, string, error) {
	interfaceNames := make([]string, 0, djiSerialLast-djiSerialFirst+1)
	interfacePaths := make([]string, 0, cap(interfaceNames))
	needsBind := false
	for index := djiSerialFirst; index <= djiSerialLast; index++ {
		name := fmt.Sprintf("%s:1.%d", usbName, index)
		path := filepath.Join(usbRoot, name)
		if _, err := os.Stat(path); err != nil {
			return false, "", fmt.Errorf("DJI serial interface %s unavailable: %w", name, err)
		}
		driver := usbInterfaceDriver(path)
		if driver != "" && driver != "option" && driver != "qmi_wwan" {
			return false, "", fmt.Errorf("refusing to replace unexpected driver %q on %s", driver, name)
		}
		interfaceNames = append(interfaceNames, name)
		interfacePaths = append(interfacePaths, path)
		if driver != "option" || firstEntryName(path, "ttyUSB") == "" {
			needsBind = true
		}
	}
	if !needsBind {
		return false, filepath.Join(devRoot, firstEntryName(interfacePaths[djiATInterface-djiSerialFirst], "ttyUSB")), nil
	}

	for index, path := range interfacePaths {
		if usbInterfaceDriver(path) != "qmi_wwan" {
			continue
		}
		if err := writeSysfs(filepath.Join(driversRoot, "qmi_wwan", "unbind"), interfaceNames[index]); err != nil {
			return false, "", fmt.Errorf("unbind qmi_wwan from serial interface %s: %w", interfaceNames[index], err)
		}
	}

	optionSerialRoot := filepath.Join(sysRoot, "bus", "usb-serial", "drivers", "option1")
	if err := writeSysfs(filepath.Join(optionSerialRoot, "new_id"), djiUSBID); err != nil && !errors.Is(err, syscall.EEXIST) {
		return false, "", fmt.Errorf("register DJI option dynamic ID: %w", err)
	}

	for index, path := range interfacePaths {
		if usbInterfaceDriver(path) == "option" {
			continue
		}
		if err := writeSysfs(filepath.Join(driversRoot, "option", "bind"), interfaceNames[index]); err != nil {
			return false, "", fmt.Errorf("bind option to %s: %w", interfaceNames[index], err)
		}
	}
	for index, path := range interfacePaths {
		if driver := usbInterfaceDriver(path); driver != "option" {
			return false, "", fmt.Errorf("serial interface %s driver is %q after option bind", interfaceNames[index], driver)
		}
	}

	serialDevices, err := waitForPrefixedNodes(ctx, interfacePaths, devRoot, "ttyUSB", djiNodeWait)
	if err != nil {
		return false, "", fmt.Errorf("option bound but not all ttyUSB nodes appeared for %s: %w", usbName, err)
	}
	return true, serialDevices[djiATInterface-djiSerialFirst], nil
}

func bindDJIQMIFunction(
	ctx context.Context,
	sysRoot, devRoot, usbRoot, driversRoot, usbName string,
) (bool, string, string, error) {
	interfaceName := fmt.Sprintf("%s:1.%d", usbName, djiQMIInterface)
	interfacePath := filepath.Join(usbRoot, interfaceName)
	if _, err := os.Stat(interfacePath); err != nil {
		return false, "", "", fmt.Errorf("DJI QMI interface %s unavailable: %w", interfaceName, err)
	}
	driver := usbInterfaceDriver(interfacePath)
	control := firstDeviceNode(filepath.Join(interfacePath, "usbmisc"), devRoot, "cdc-wdm")
	network := firstEntryName(filepath.Join(interfacePath, "net"), "")
	if driver == "qmi_wwan" && control != "" {
		return false, control, network, nil
	}
	if !qmiWWANClass(interfacePath) {
		// CDC/ECM/MBIM functions are not QMI. Periodic discovery used to bind
		// qmi_wwan anyway, which fails with EBUSY (-16) every 30s and glitches AT.
		return false, "", network, nil
	}
	if driver != "" && driver != "option" && driver != "qmi_wwan" {
		return false, "", "", fmt.Errorf("refusing to replace unexpected driver %q on %s", driver, interfaceName)
	}

	if driver != "" {
		if err := writeSysfs(filepath.Join(driversRoot, driver, "unbind"), interfaceName); err != nil {
			return false, "", "", fmt.Errorf("unbind %s from %s: %w", driver, interfaceName, err)
		}
	}

	// Do not assert DTR here. vocat doctor does that once. Discovery runs every
	// 30s; usbfs control transfers on an unclaimed if4 log "did not claim
	// interface 4" and stall the same USB device's AT port (CMGS timeouts,
	// +CMS ERROR: 350).
	qmiDriverRoot := filepath.Join(driversRoot, "qmi_wwan")
	if err := bindDJIQMIInterface(qmiDriverRoot, interfacePath, interfaceName); err != nil {
		return false, "", "", err
	}
	nodes, err := waitForPrefixedNodes(ctx, []string{filepath.Join(interfacePath, "usbmisc")}, devRoot, "cdc-wdm", djiNodeWait)
	if err != nil {
		return false, "", "", fmt.Errorf("qmi_wwan bound but no cdc-wdm node appeared for %s: %w", interfaceName, err)
	}
	return true, nodes[0], firstEntryName(filepath.Join(interfacePath, "net"), ""), nil
}

func qmiWWANClass(interfacePath string) bool {
	raw, err := readTrimmedFile(filepath.Join(interfacePath, "bInterfaceClass"))
	if err != nil || raw == "" {
		return true
	}
	value, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(raw), "0x"), 16, 8)
	if err != nil {
		return true
	}
	switch value {
	case 0x02, 0x0a, 0x0e:
		return false
	default:
		return true
	}
}

func bindDJIQMIInterface(driverRoot, interfacePath, interfaceName string) (returnErr error) {
	bindPath := filepath.Join(driverRoot, "bind")
	dynamicIDAdded := false
	defer func() {
		if dynamicIDAdded {
			removeErr := removeDynamicUSBID(driverRoot, djiUSBID)
			if returnErr == nil && removeErr != nil {
				returnErr = fmt.Errorf("remove temporary DJI qmi_wwan dynamic ID: %w", removeErr)
			}
		}
	}()
	if err := writeSysfs(bindPath, interfaceName); err != nil {
		newIDErr := writeSysfs(filepath.Join(driverRoot, "new_id"), djiUSBID)
		if newIDErr != nil && !errors.Is(newIDErr, syscall.EEXIST) {
			return fmt.Errorf("register DJI qmi_wwan dynamic ID after bind failure %v: %w", err, newIDErr)
		}
		dynamicIDAdded = true
		if usbInterfaceDriver(interfacePath) != "qmi_wwan" {
			if retryErr := writeSysfs(bindPath, interfaceName); retryErr != nil {
				return fmt.Errorf("bind qmi_wwan to %s: %w", interfaceName, retryErr)
			}
		}
	}
	if driver := usbInterfaceDriver(interfacePath); driver != "qmi_wwan" {
		return fmt.Errorf("interface %s driver is %q after qmi_wwan bind", interfaceName, driver)
	}
	return nil
}

func ensureUSBDriverLoaded(ctx context.Context, driversRoot, driverName, moduleName string) error {
	if _, err := os.Stat(filepath.Join(driversRoot, driverName)); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect %s driver: %w", driverName, err)
	}
	sysRoot := filepath.Clean(filepath.Dir(filepath.Dir(filepath.Dir(driversRoot))))
	if sysRoot != "/sys" {
		return fmt.Errorf("%s driver is unavailable", driverName)
	}
	modprobe, err := exec.LookPath("modprobe")
	if err != nil {
		return fmt.Errorf("%s is not loaded and modprobe is unavailable", driverName)
	}
	if output, loadErr := exec.CommandContext(ctx, modprobe, moduleName).CombinedOutput(); loadErr != nil {
		return fmt.Errorf("load %s: %w: %s", moduleName, loadErr, strings.TrimSpace(string(output)))
	}
	if _, err := os.Stat(filepath.Join(driversRoot, driverName)); err != nil {
		return fmt.Errorf("%s driver is unavailable after loading module %s: %w", driverName, moduleName, err)
	}
	return nil
}

func removeDynamicUSBID(driverRoot, id string) error {
	path := filepath.Join(driverRoot, "remove_id")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := writeSysfs(path, id); err != nil && !errors.Is(err, syscall.ENODEV) && !errors.Is(err, syscall.ENOENT) {
		return err
	}
	return nil
}

func waitForPrefixedNodes(ctx context.Context, roots []string, devRoot, prefix string, timeout time.Duration) ([]string, error) {
	deadline := time.Now().Add(timeout)
	nodes := make([]string, len(roots))
	for {
		complete := true
		for index, root := range roots {
			name := firstEntryName(root, prefix)
			if name == "" {
				complete = false
				continue
			}
			nodes[index] = filepath.Join(devRoot, name)
		}
		if complete {
			return nodes, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out")
		}
		time.Sleep(djiNodePollEvery)
	}
}

func usbDeviceNode(devicePath, devRoot string) (string, error) {
	busNumber, err := readUSBNumber(filepath.Join(devicePath, "busnum"))
	if err != nil {
		return "", err
	}
	deviceNumber, err := readUSBNumber(filepath.Join(devicePath, "devnum"))
	if err != nil {
		return "", err
	}
	return filepath.Join(devRoot, "bus", "usb", fmt.Sprintf("%03d", busNumber), fmt.Sprintf("%03d", deviceNumber)), nil
}

func assertUSBDTR(devicePath string, interfaceIndex int) error {
	fd, err := unix.Open(devicePath, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open USB device %s: %w", devicePath, err)
	}
	defer unix.Close(fd)
	if err := setUSBControlLineState(fd, interfaceIndex, false); err != nil {
		return fmt.Errorf("clear CDC DTR on %s interface %d: %w", devicePath, interfaceIndex, err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := setUSBControlLineState(fd, interfaceIndex, true); err != nil {
		return fmt.Errorf("assert CDC DTR on %s interface %d: %w", devicePath, interfaceIndex, err)
	}
	time.Sleep(time.Second)
	return nil
}

func setUSBControlLineState(fd, interfaceIndex int, dtr bool) error {
	var value uint16
	if dtr {
		value = 1
	}
	transfer := usbControlTransfer{
		RequestType: 0x21,
		Request:     0x22,
		Value:       value,
		Index:       uint16(interfaceIndex),
		Timeout:     5000,
	}
	const ioctlDirectionReadWrite = uintptr(3)
	request := ioctlDirectionReadWrite<<30 |
		uintptr(unsafe.Sizeof(transfer))<<16 |
		uintptr('U')<<8
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), request, uintptr(unsafe.Pointer(&transfer)))
	if errno != 0 {
		return errno
	}
	return nil
}

func readTrimmedFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readUSBNumber(path string) (int, error) {
	value, err := readTrimmedFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 || number > 999 {
		return 0, fmt.Errorf("invalid %s %q", filepath.Base(path), value)
	}
	return number, nil
}

func usbInterfaceDriver(interfacePath string) string {
	resolved, err := filepath.EvalSymlinks(filepath.Join(interfacePath, "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(resolved)
}

var sysfsControlWriter = writeSysfsFile

func writeSysfs(path, value string) error {
	return sysfsControlWriter(path, value)
}

func writeSysfsFile(path, value string) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(value)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func firstDeviceNode(directory, devRoot, prefix string) string {
	name := firstEntryName(directory, prefix)
	if name == "" {
		return ""
	}
	return filepath.Join(devRoot, name)
}

func firstEntryName(directory, prefix string) string {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			return entry.Name()
		}
	}
	return ""
}

func ensureDJIUSBComposition(ctx context.Context, sysRoot, devRoot string) error {
	_, err := EnsureDJIUSBComposition(ctx, sysRoot, devRoot)
	return err
}
