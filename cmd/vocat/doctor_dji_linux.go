//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"vocat/internal/modem"
)

const (
	djiVendorID  = "2ca3"
	djiProductID = "4006"
	djiQMIIndex  = 4
)

type usbControlTransfer struct {
	RequestType uint8
	Request     uint8
	Value       uint16
	Index       uint16
	Length      uint16
	Timeout     uint32
	Data        uintptr
}

func repairDJIQMI(ctx context.Context) (djiQMIRepairResult, error) {
	qmicli, err := exec.LookPath("qmicli")
	if err != nil {
		return djiQMIRepairResult{}, errors.New("qmicli is required to verify DJI QMI readiness; install libqmi-utils on Debian/Ubuntu/Fedora, libqmi on Arch Linux, or qmi-utils on Alpine")
	}
	return retryDJIQMI(ctx, 3, 500*time.Millisecond, func(attemptContext context.Context) (djiQMIRepairResult, error) {
		return repairDJIQMIAt(attemptContext, "/sys", "/dev", qmicli)
	})
}

func retryDJIQMI(
	ctx context.Context,
	maxAttempts int,
	delay time.Duration,
	attempt func(context.Context) (djiQMIRepairResult, error),
) (djiQMIRepairResult, error) {
	var result djiQMIRepairResult
	var err error
	for attemptNumber := 1; attemptNumber <= maxAttempts; attemptNumber++ {
		result, err = attempt(ctx)
		result.Attempts = attemptNumber
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			break
		}
		timer := time.NewTimer(time.Duration(attemptNumber) * delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, errors.Join(err, ctx.Err())
		case <-timer.C:
		}
	}
	return result, fmt.Errorf("failed after %d DTR repair attempt(s): %w", result.Attempts, err)
}

func repairDJIQMIAt(ctx context.Context, sysRoot, devRoot, qmicli string) (result djiQMIRepairResult, returnErr error) {
	statuses, err := modem.EnsureDJIUSBComposition(ctx, sysRoot, devRoot)
	if err != nil {
		return result, err
	}
	if len(statuses) != 1 {
		return result, fmt.Errorf("expected exactly one DJI %s:%s USB device, found %d", djiVendorID, djiProductID, len(statuses))
	}
	status := statuses[0]
	result.USBName = status.USBName
	result.Interface = status.QMIInterface
	result.USBDevice = status.USBDevice
	result.ATDevice = status.ATDevice
	result.ControlDevice = status.ControlDevice
	result.NetworkInterface = status.NetworkInterface
	if result.USBDevice == "" {
		return result, fmt.Errorf("DJI USB device node for %s is unavailable", status.USBName)
	}
	if err := assertUSBDTR(result.USBDevice, djiQMIIndex); err != nil {
		return result, err
	}
	if result.ControlDevice == "" {
		return result, fmt.Errorf("qmi_wwan bound but no cdc-wdm node appeared for %s", result.Interface)
	}
	time.Sleep(250 * time.Millisecond)
	probeContext, cancelProbe := context.WithTimeout(ctx, 8*time.Second)
	output, probeErr := exec.CommandContext(probeContext, qmicli, "-d", result.ControlDevice, "--dms-get-operating-mode").CombinedOutput()
	probeContextErr := probeContext.Err()
	cancelProbe()
	result.QMIProbe = strings.TrimSpace(string(output))
	if probeErr != nil {
		if probeContextErr != nil {
			probeErr = errors.Join(probeErr, probeContextErr)
		}
		return result, fmt.Errorf("DMS readiness check after DTR repair: %w: %s", probeErr, result.QMIProbe)
	}
	return result, nil
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
	// QDC507 acknowledges the control transfer before its QMI firmware is ready.
	time.Sleep(time.Second)
	return nil
}

func setUSBControlLineState(fd, interfaceIndex int, dtr bool) error {
	var value uint16
	if dtr {
		value = 1 // USB_CDC_CTRL_DTR
	}
	transfer := usbControlTransfer{
		RequestType: 0x21, // host-to-device, class, interface
		Request:     0x22, // USB_CDC_REQ_SET_CONTROL_LINE_STATE
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
