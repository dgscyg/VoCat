package device

import (
	"net"
	"regexp"
	"strings"

	"vocat/internal/modem"
)

type cellularLease struct {
	Address net.IP
	Mask    net.IPMask
	Gateway net.IP
	DNS     []string
}

var qmiSettingsField = regexp.MustCompile(`(?i)^\s*([^:]+):\s*(.+?)\s*$`)

func parseCGCONTRDP(response modem.Response) (cellularLease, bool) {
	for _, line := range response.Lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		if !strings.HasPrefix(upper, "+CGCONTRDP:") {
			continue
		}
		fields := csvValues(strings.TrimSpace(trimmed[len("+CGCONTRDP:"):]))
		if len(fields) < 4 {
			continue
		}
		lease, ok := leaseFromPDPFields(fields[3], optionalField(fields, 4), optionalField(fields, 5), optionalField(fields, 6))
		if ok {
			return lease, true
		}
	}
	return cellularLease{}, false
}

func parseCGPADDR(response modem.Response) (cellularLease, bool) {
	for _, line := range response.Lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		if !strings.HasPrefix(upper, "+CGPADDR:") {
			continue
		}
		fields := csvValues(strings.TrimSpace(trimmed[len("+CGPADDR:"):]))
		if len(fields) < 2 {
			continue
		}
		address := net.ParseIP(strings.Trim(fields[1], `"`)).To4()
		if address == nil {
			continue
		}
		return cellularLease{Address: address, Mask: net.CIDRMask(32, 32)}, true
	}
	return cellularLease{}, false
}

func parseQMICurrentSettings(output string) (cellularLease, bool) {
	values := map[string]string{}
	for _, raw := range strings.Split(output, "\n") {
		match := qmiSettingsField.FindStringSubmatch(raw)
		if len(match) != 3 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(match[1]))
		value := strings.Trim(strings.TrimSpace(match[2]), "'\"")
		values[key] = value
	}
	address := net.ParseIP(values["ipv4 address"]).To4()
	maskIP := net.ParseIP(values["ipv4 subnet mask"]).To4()
	if address == nil {
		return cellularLease{}, false
	}
	mask := net.CIDRMask(32, 32)
	if maskIP != nil {
		mask = net.IPMask(maskIP)
	}
	lease := cellularLease{Address: address, Mask: mask, Gateway: net.ParseIP(values["ipv4 gateway address"]).To4()}
	for _, key := range []string{"ipv4 primary dns", "ipv4 secondary dns"} {
		if server := net.ParseIP(values[key]); server != nil {
			lease.DNS = append(lease.DNS, server.String())
		}
	}
	return lease, true
}

func leaseFromPDPFields(addressAndMask, gateway, dns1, dns2 string) (cellularLease, bool) {
	address, mask, ok := parsePDPAddressAndMask(addressAndMask)
	if !ok {
		return cellularLease{}, false
	}
	lease := cellularLease{Address: address, Mask: mask, Gateway: net.ParseIP(strings.Trim(gateway, `"`)).To4()}
	for _, server := range []string{dns1, dns2} {
		if ip := net.ParseIP(strings.Trim(server, `"`)); ip != nil {
			lease.DNS = append(lease.DNS, ip.String())
		}
	}
	return lease, true
}

func parsePDPAddressAndMask(value string) (net.IP, net.IPMask, bool) {
	value = strings.Trim(strings.TrimSpace(value), `"`)
	if value == "" {
		return nil, nil, false
	}
	parts := strings.Split(value, ".")
	if len(parts) == 8 {
		address := net.ParseIP(strings.Join(parts[:4], ".")).To4()
		maskIP := net.ParseIP(strings.Join(parts[4:], ".")).To4()
		if address == nil || maskIP == nil {
			return nil, nil, false
		}
		return address, net.IPMask(maskIP), true
	}
	if address := net.ParseIP(value).To4(); address != nil {
		return address, net.CIDRMask(32, 32), true
	}
	return nil, nil, false
}

func optionalField(fields []string, index int) string {
	if index >= len(fields) {
		return ""
	}
	return fields[index]
}

func (lease cellularLease) prefixLen() int {
	ones, bits := lease.Mask.Size()
	if bits != 32 || ones < 0 {
		return 32
	}
	return ones
}

// nextHop is the address that marked sockets should use as the cellular
// default gateway. Carrier "router" fields are often a public DNS/NAT address
// outside the /30; ARP for that address on wwan0 fails with "no route to host".
func (lease cellularLease) nextHop() net.IP {
	address := lease.Address.To4()
	if address == nil {
		return nil
	}
	mask := lease.Mask
	if mask == nil {
		mask = net.CIDRMask(32, 32)
	}
	if gateway := lease.Gateway.To4(); gateway != nil && sameIPv4Network(address, gateway, mask) {
		return gateway
	}
	return pointToPointPeer(address, mask)
}

func sameIPv4Network(left, right net.IP, mask net.IPMask) bool {
	left4, right4 := left.To4(), right.To4()
	if left4 == nil || right4 == nil || mask == nil {
		return false
	}
	return left4.Mask(mask).Equal(right4.Mask(mask))
}

func pointToPointPeer(address net.IP, mask net.IPMask) net.IP {
	ip := address.To4()
	if ip == nil || mask == nil {
		return nil
	}
	ones, bits := mask.Size()
	if bits != 32 {
		return nil
	}
	network := ip.Mask(mask)
	switch ones {
	case 31:
		peer := append(net.IP(nil), ip...)
		peer[3] ^= 1
		return peer
	case 30:
		first := append(net.IP(nil), network...)
		first[3] |= 1
		second := append(net.IP(nil), network...)
		second[3] |= 2
		switch {
		case ip.Equal(first):
			return second
		case ip.Equal(second):
			return first
		}
	}
	return nil
}
