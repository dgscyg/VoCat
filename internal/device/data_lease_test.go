package device

import (
	"net"
	"testing"

	"vocat/internal/modem"
)

func TestParseCGCONTRDPCombinedAddressAndMask(t *testing.T) {
	lease, ok := parseCGCONTRDP(okResponse(`+CGCONTRDP: 1,5,"internet","10.46.1.2.255.255.255.252","10.46.1.1","8.8.8.8","1.1.1.1"`))
	if !ok {
		t.Fatal("parseCGCONTRDP() = false")
	}
	if lease.Address.String() != "10.46.1.2" || lease.prefixLen() != 30 {
		t.Fatalf("address/prefix = %s/%d", lease.Address, lease.prefixLen())
	}
	if lease.Gateway.String() != "10.46.1.1" {
		t.Fatalf("gateway = %s", lease.Gateway)
	}
	if len(lease.DNS) != 2 || lease.DNS[0] != "8.8.8.8" || lease.DNS[1] != "1.1.1.1" {
		t.Fatalf("dns = %#v", lease.DNS)
	}
}

func TestParseCGPADDR(t *testing.T) {
	lease, ok := parseCGPADDR(okResponse(`+CGPADDR: 1,"10.8.0.14"`))
	if !ok || lease.Address.String() != "10.8.0.14" || lease.prefixLen() != 32 {
		t.Fatalf("lease = %#v ok=%v", lease, ok)
	}
}

func TestParseQMICurrentSettings(t *testing.T) {
	lease, ok := parseQMICurrentSettings(`
[/dev/cdc-wdm0] Current settings retrieved:
           IP Family: IPv4
        IPv4 address: 10.123.45.67
    IPv4 subnet mask: 255.255.255.252
IPv4 gateway address: 10.123.45.68
    IPv4 primary DNS: 218.2.2.2
  IPv4 secondary DNS: 218.4.4.4
                 MTU: 1500
`)
	if !ok {
		t.Fatal("parseQMICurrentSettings() = false")
	}
	if lease.Address.String() != "10.123.45.67" || net.IP(lease.Mask).String() != "255.255.255.252" {
		t.Fatalf("lease = %#v", lease)
	}
	if lease.Gateway.String() != "10.123.45.68" {
		t.Fatalf("gateway = %s", lease.Gateway)
	}
	if len(lease.DNS) != 2 || lease.DNS[0] != "218.2.2.2" {
		t.Fatalf("dns = %#v", lease.DNS)
	}
}

func TestParseCGCONTRDPRejectsEmpty(t *testing.T) {
	if _, ok := parseCGCONTRDP(modem.Response{Lines: []string{"OK"}}); ok {
		t.Fatal("empty CGCONTRDP was accepted")
	}
}
