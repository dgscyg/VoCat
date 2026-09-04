//go:build linux

package exportproxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode"

	"vocat/internal/fwmark"
	"vocat/internal/hostif"
)

func platformSupported() error { return nil }

func prepareInterfaceDNS(networkInterface string) {
	fwmark.EnsureDNSBypass(exportRouteMark(networkInterface))
}

func boundDialer(networkInterface string) net.Dialer {
	return net.Dialer{Control: func(_, _ string, raw syscall.RawConn) error {
		var bindError error
		err := raw.Control(func(fd uintptr) {
			if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, int(exportRouteMark(networkInterface))); err != nil {
				bindError = err
				return
			}
			bindError = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, networkInterface)
		})
		if err != nil {
			return err
		}
		return bindError
	}}
}

func exportRouteMark(networkInterface string) uint32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(networkInterface))
	return 0x56000000 | (hash.Sum32() & 0x00ffffff)
}

func interfaceDialReady(networkInterface string) error {
	networkInterface = strings.TrimSpace(networkInterface)
	if networkInterface == "" {
		return fmt.Errorf("cellular network interface is required")
	}
	var iface *net.Interface
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		iface, err = hostif.Lookup(networkInterface)
		if err == nil {
			break
		}
		time.Sleep(125 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("%s is not present on this host: %w", networkInterface, err)
	}
	if iface.Flags&net.FlagUp == 0 {
		return fmt.Errorf("%s is down; enable roaming data so VoCat can assign a protected IPv4 address before public-IP or export-proxy traffic can leave the modem", networkInterface)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return fmt.Errorf("read %s addresses: %w", networkInterface, err)
	}
	for _, addr := range addrs {
		var ip net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		}
		if ip.To4() != nil && !ip.IsLinkLocalUnicast() {
			return nil
		}
	}
	return fmt.Errorf("%s is up but has no IPv4 address; AT+CGACT alone does not configure the host interface", networkInterface)
}

func lookupBoundIPs(ctx context.Context, networkInterface, host string) ([]net.IPAddr, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IPAddr{{IP: ip}}, nil
	}
	if err := interfaceDialReady(networkInterface); err != nil {
		return nil, err
	}
	fwmark.EnsureDNSBypass(exportRouteMark(networkInterface))
	dialer := boundDialer(networkInterface)
	var lastError error
	for _, server := range exportRouteDNSServers(networkInterface) {
		resolved, queryErr := dnsQueryA(ctx, &dialer, server, host)
		if queryErr == nil && len(resolved) > 0 {
			return ipsToAddrs(resolved), nil
		}
		lastError = queryErr
	}
	// Bound UDP to 1.1.1.1/8.8.8.8 often fake-handshakes on CN cellular.
	// Clash/host DNS still works for unmarked queries; dial the answers with
	// SO_BINDTODEVICE so the HTTP probe still exits through the modem.
	if ips, hostErr := lookupHostIPv4(ctx, host); hostErr == nil && len(ips) > 0 {
		return ips, nil
	} else if lastError == nil {
		lastError = hostErr
	}
	if ips, dohErr := lookupDoH(ctx, networkInterface, host); dohErr == nil && len(ips) > 0 {
		return ips, nil
	} else if lastError == nil {
		lastError = dohErr
	}
	return nil, fmt.Errorf("lookup %s through %s: %w", host, networkInterface, lastError)
}

func lookupHostIPv4(ctx context.Context, host string) ([]net.IPAddr, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return nil, err
	}
	return ipsToAddrs(ips), nil
}

func ipsToAddrs(ips []net.IP) []net.IPAddr {
	result := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		result = append(result, net.IPAddr{IP: ip})
	}
	return result
}

func lookupDoH(ctx context.Context, networkInterface, host string) ([]net.IPAddr, error) {
	dialer := boundDialer(networkInterface)
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialTarget(ctx, address, &dialer, networkInterface)
		},
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 8 * time.Second,
		TLSHandshakeTimeout:   8 * time.Second,
	}
	defer transport.CloseIdleConnections()
	var lastError error
	for _, rawURL := range []string{
		"https://1.1.1.1/dns-query?name=" + url.QueryEscape(host) + "&type=A",
		"https://8.8.8.8/resolve?name=" + url.QueryEscape(host) + "&type=A",
	} {
		ips, err := queryDoH(ctx, transport, rawURL)
		if err == nil && len(ips) > 0 {
			return ipsToAddrs(ips), nil
		}
		lastError = err
	}
	if lastError == nil {
		lastError = errors.New("no DoH answers")
	}
	return nil, lastError
}

func queryDoH(ctx context.Context, transport *http.Transport, rawURL string) ([]net.IP, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/dns-json")
	request.Header.Set("User-Agent", "VoCat/1.0")
	response, err := transport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("doh %s returned HTTP %d", request.URL.Host, response.StatusCode)
	}
	return decodeDNSJSON(io.LimitReader(response.Body, 64<<10))
}

func dnsQueryA(ctx context.Context, dialer *net.Dialer, server, name string) ([]net.IP, error) {
	ips, err := dnsQueryAOn(ctx, dialer, "udp4", server, name, 512)
	if err == nil {
		return ips, nil
	}
	return dnsQueryAOn(ctx, dialer, "tcp4", server, name, 4096)
}

func dnsQueryAOn(ctx context.Context, dialer *net.Dialer, network, server, name string, readSize int) ([]net.IP, error) {
	payload, id, err := encodeDNSQueryA(name)
	if err != nil {
		return nil, err
	}
	conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(server, "53"))
	if err != nil {
		return nil, fmt.Errorf("dial %s %s:53: %w", network, server, err)
	}
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	if strings.HasPrefix(network, "tcp") {
		var header [2]byte
		binary.BigEndian.PutUint16(header[:], uint16(len(payload)))
		if _, err := conn.Write(append(header[:], payload...)); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return nil, err
		}
		length := int(binary.BigEndian.Uint16(header[:]))
		if length < 12 || length > 65535 {
			return nil, errors.New("invalid tcp dns length")
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return nil, err
		}
		return parseDNSResponseA(buf, id, name)
	}
	if _, err := conn.Write(payload); err != nil {
		return nil, err
	}
	buf := make([]byte, readSize)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return parseDNSResponseA(buf[:n], id, name)
}

func exportRouteDNSServers(networkInterface string) []string {
	if !validInterfaceName(networkInterface) {
		return []string{"1.1.1.1", "8.8.8.8"}
	}
	root, err := os.OpenRoot("/run/vocat")
	if err != nil {
		return []string{"1.1.1.1", "8.8.8.8"}
	}
	defer root.Close()
	file, err := root.Open("cellular-" + networkInterface + ".dns")
	if err != nil {
		return []string{"1.1.1.1", "8.8.8.8"}
	}
	defer file.Close()
	servers := make([]string, 0, 2)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if ip := net.ParseIP(strings.TrimSpace(scanner.Text())); ip.To4() != nil {
			servers = append(servers, ip.To4().String())
		}
	}
	if len(servers) == 0 {
		return []string{"1.1.1.1", "8.8.8.8"}
	}
	return appendPublicResolverFallbacks(servers)
}

func appendPublicResolverFallbacks(servers []string) []string {
	seen := make(map[string]bool, len(servers)+2)
	out := make([]string, 0, len(servers)+2)
	for _, server := range servers {
		if seen[server] {
			continue
		}
		seen[server] = true
		out = append(out, server)
	}
	for _, fallback := range []string{"1.1.1.1", "8.8.8.8"} {
		if !seen[fallback] {
			out = append(out, fallback)
		}
	}
	return out
}

// Linux IFNAMSIZ is 16 including the terminator. Restricting names here both
// matches kernel interface names and prevents a stored device value from ever
// becoming a filesystem path component.
func validInterfaceName(value string) bool {
	if value == "" || len(value) > 15 || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if character > unicode.MaxASCII || !(character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == '.') {
			return false
		}
	}
	return true
}
