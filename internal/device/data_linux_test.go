//go:build linux

package device

import (
	"errors"
	"syscall"
	"testing"
)

func TestQmiCallInUse(t *testing.T) {
	if !qmiCallInUse("interface-in-use-config-match") {
		t.Fatal("interface-in-use was ignored")
	}
	if qmiCallInUse("already started") {
		t.Fatal("already started was treated as in-use")
	}
}

func TestReadOnlySysfsHint(t *testing.T) {
	if hint := readOnlySysfsHint(syscall.EROFS); hint == "" {
		t.Fatal("EROFS produced no sysfs hint")
	}
	if hint := readOnlySysfsHint(errors.New("open /sys/class/net/wwan0/qmi/raw_ip: read-only file system")); hint == "" {
		t.Fatal("read-only file system produced no sysfs hint")
	}
	if hint := readOnlySysfsHint(syscall.EBUSY); hint != "" {
		t.Fatalf("EBUSY produced unexpected hint %q", hint)
	}
}

func TestIsNoRouteError(t *testing.T) {
	if !isNoRouteError(syscall.EHOSTUNREACH) {
		t.Fatal("EHOSTUNREACH was not treated as no-route")
	}
	if !isNoRouteError(syscall.ENETUNREACH) {
		t.Fatal("ENETUNREACH was not treated as no-route")
	}
	if !isNoRouteError(errors.New("dial tcp4 1.1.1.1:443: connect: no route to host")) {
		t.Fatal("wrapped no route to host was ignored")
	}
	if isNoRouteError(errors.New("i/o timeout")) {
		t.Fatal("timeout was treated as no-route")
	}
}
