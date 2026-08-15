//go:build linux

package exportproxy

import (
	"strings"
	"testing"
)

func TestInterfaceDialReadyMissingInterface(t *testing.T) {
	err := interfaceDialReady("wwan-missing-vocat")
	if err == nil {
		t.Fatal("missing interface was treated as ready")
	}
	if !strings.Contains(err.Error(), "wwan-missing-vocat") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidInterfaceName(t *testing.T) {
	for _, value := range []string{"wwan0", "wwp0s20f0u5i4", "rmnet_data0", "usb.1"} {
		if !validInterfaceName(value) {
			t.Errorf("validInterfaceName(%q) = false", value)
		}
	}
	for _, value := range []string{"", ".", "..", "../wwan0", `..\wwan0`, "wwan0/evil", "interface-name-too-long"} {
		if validInterfaceName(value) {
			t.Errorf("validInterfaceName(%q) = true", value)
		}
	}
}
