//go:build linux

package device

import (
	"errors"
	"syscall"
	"testing"
)

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
