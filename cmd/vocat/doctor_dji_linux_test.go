//go:build linux

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestDJIUSBControlTransferLayout(t *testing.T) {
	var transfer usbControlTransfer
	if got := unsafe.Sizeof(transfer); got != 24 {
		t.Fatalf("usbControlTransfer size = %d, want 24", got)
	}
	if transfer.RequestType != 0 || transfer.Request != 0 {
		t.Fatal("zero-value transfer unexpectedly initialized")
	}
}

func TestRepairDJIQMIRequiresQMICLIBeforeUSBAccess(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := repairDJIQMI(context.Background())
	if err == nil {
		t.Fatal("repairDJIQMI() unexpectedly succeeded without qmicli")
	}
	if !strings.Contains(err.Error(), "qmicli is required") || !strings.Contains(err.Error(), "libqmi-utils") {
		t.Fatalf("repairDJIQMI() error = %q, want an actionable qmicli prerequisite error", err)
	}
	if strings.Contains(err.Error(), "DTR repair attempt") || strings.Contains(err.Error(), "USB topology") {
		t.Fatalf("repairDJIQMI() touched the repair path before checking qmicli: %v", err)
	}
}

func TestRetryDJIQMISucceedsAfterTransientFailures(t *testing.T) {
	attempts := 0
	result, err := retryDJIQMI(context.Background(), 3, time.Millisecond, func(context.Context) (djiQMIRepairResult, error) {
		attempts++
		if attempts < 3 {
			return djiQMIRepairResult{}, errors.New("transient QMI timeout")
		}
		return djiQMIRepairResult{ControlDevice: "/dev/cdc-wdm0"}, nil
	})
	if err != nil {
		t.Fatalf("retryDJIQMI() error = %v", err)
	}
	if attempts != 3 || result.Attempts != 3 {
		t.Fatalf("attempts = %d, result.Attempts = %d, want 3", attempts, result.Attempts)
	}
}

func TestRetryDJIQMIStopsAfterBoundedAttempts(t *testing.T) {
	attempts := 0
	_, err := retryDJIQMI(context.Background(), 2, time.Millisecond, func(context.Context) (djiQMIRepairResult, error) {
		attempts++
		return djiQMIRepairResult{}, errors.New("persistent failure")
	})
	if err == nil {
		t.Fatal("retryDJIQMI() unexpectedly succeeded")
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}
