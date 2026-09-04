package device

import (
	"testing"

	"vocat/internal/modem"
)

func TestNMEAPortPicksNMEARole(t *testing.T) {
	got := nmeaPort(modem.Candidate{Ports: []modem.Port{
		{Path: "/dev/ttyUSB0", Role: modem.PortRoleDiagnostic},
		{Path: "/dev/ttyUSB1", Role: modem.PortRoleNMEA},
		{Path: "/dev/ttyUSB2", Role: modem.PortRoleAT},
	}})
	if got.Path != "/dev/ttyUSB1" {
		t.Fatalf("nmea port = %#v", got)
	}
	if nmeaPort(modem.Candidate{}).Path != "" {
		t.Fatal("empty candidate returned an NMEA port")
	}
}
