//go:build linux

package device

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vocat/internal/modem"
)

func setQMINetwork(
	ctx context.Context,
	candidate modem.Candidate,
	enabled bool,
	apn string,
	ipVersion string,
	username string,
	password string,
	authentication string,
	atClient modem.Client,
) (NetworkResult, error) {
	qmiNetwork, err := exec.LookPath("qmi-network")
	if err != nil {
		return NetworkResult{}, fmt.Errorf("%w: install libqmi-utils to control %s", ErrDataBackendUnavailable, candidate.QMIControl)
	}
	profile, err := os.CreateTemp("", "vocat-qmi-*.conf")
	if err != nil {
		return NetworkResult{}, fmt.Errorf("create temporary QMI profile: %w", err)
	}
	profilePath := profile.Name()
	defer os.Remove(profilePath)
	ipType := map[string]string{"IP": "4", "IPV6": "6", "IPV4V6": "4"}[ipVersion]
	profileText := fmt.Sprintf("IP_TYPE=%s\nPROXY=yes\n", ipType)
	if apn != "" {
		profileText = "APN=" + apn + "\n" + profileText
	}
	if username != "" {
		profileText += "APN_USER=" + shellProfileValue(username) + "\n"
	}
	if password != "" {
		profileText += "APN_PASS=" + shellProfileValue(password) + "\n"
	}
	if authentication != "" && authentication != "NONE" {
		profileText += "APN_AUTH=" + shellProfileValue(strings.ToLower(authentication)) + "\n"
	}
	if _, err := fmt.Fprint(profile, profileText); err != nil {
		_ = profile.Close()
		return NetworkResult{}, fmt.Errorf("write temporary QMI profile: %w", err)
	}
	if err := profile.Chmod(0o600); err != nil {
		_ = profile.Close()
		return NetworkResult{}, fmt.Errorf("protect temporary QMI profile: %w", err)
	}
	if err := profile.Close(); err != nil {
		return NetworkResult{}, fmt.Errorf("close temporary QMI profile: %w", err)
	}

	ipCommand, lookErr := exec.LookPath("ip")
	if lookErr != nil {
		return NetworkResult{}, fmt.Errorf("%w: install iproute2 to control %s", ErrDataBackendUnavailable, candidate.NetworkInterface)
	}
	if enabled {
		if err := ensureQMIRawIP(ctx, ipCommand, candidate.NetworkInterface); err != nil {
			return NetworkResult{}, err
		}
	}
	action := "stop"
	if enabled {
		action = "start"
	}
	command := exec.CommandContext(ctx, qmiNetwork, "--profile="+profilePath, candidate.QMIControl, action)
	output, err := command.CombinedOutput()
	detail := strings.TrimSpace(string(output))
	if err != nil {
		lowerDetail := strings.ToLower(detail)
		idempotentStop := !enabled && (strings.Contains(lowerDetail, "already stopped") ||
			strings.Contains(lowerDetail, "not started") || strings.Contains(lowerDetail, "no network"))
		idempotentStart := enabled && (strings.Contains(lowerDetail, "already started") ||
			strings.Contains(lowerDetail, "already connected"))
		if !idempotentStop && !idempotentStart {
			return NetworkResult{}, fmt.Errorf("qmi-network %s failed: %w: %s", action, err, detail)
		}
	}
	if enabled {
		if err := ensureQMIRawIP(ctx, ipCommand, candidate.NetworkInterface); err != nil {
			return NetworkResult{}, err
		}
	}
	linkAction := "down"
	if enabled {
		linkAction = "up"
	}
	linkOutput, linkErr := exec.CommandContext(ctx, ipCommand, "link", "set", "dev", candidate.NetworkInterface, linkAction).CombinedOutput()
	if linkErr != nil {
		return NetworkResult{}, fmt.Errorf("set %s %s: %w: %s", candidate.NetworkInterface, linkAction, linkErr, strings.TrimSpace(string(linkOutput)))
	}
	if enabled {
		hostDetail, hostErr := bringUpExportProxyInterface(ctx, candidate, atClient, []string{"qmi-wds", "dhcp", "at-pdp"})
		if hostErr != nil {
			rollbackCtx, cancelRollback := context.WithTimeout(context.Background(), managerCommandCleanupTimeout)
			defer cancelRollback()
			releaseCellularHost(rollbackCtx, candidate.NetworkInterface)
			_, _ = exec.CommandContext(rollbackCtx, qmiNetwork, "--profile="+profilePath, candidate.QMIControl, "stop").CombinedOutput()
			_, _ = exec.CommandContext(rollbackCtx, ipCommand, "link", "set", "dev", candidate.NetworkInterface, "down").CombinedOutput()
			return NetworkResult{}, fmt.Errorf("QMI session started but the host interface has no IPv4: %w", hostErr)
		}
		detail = strings.TrimSpace(detail + "\n" + hostDetail)
	} else {
		releaseCellularHost(ctx, candidate.NetworkInterface)
	}
	return NetworkResult{
		Enabled:       enabled,
		Backend:       "qmi",
		Interface:     candidate.NetworkInterface,
		ControlDevice: candidate.QMIControl,
		APN:           apn,
		IPVersion:     ipVersion,
		Detail:        detail,
	}, nil
}

func shellProfileValue(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// exportProxyRouteIdentity must stay in sync with the Export Proxy plugin's
// Linux socket mark. Unmarked host traffic never sees the cellular default
// route; only plugin sockets carrying this mark are policy-routed to it.
func exportProxyRouteIdentity(networkInterface string) (mark uint32, table, priority int) {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(networkInterface))
	value := hash.Sum32()
	mark = 0x56000000 | (value & 0x00ffffff)
	table = 20000 + int(value%10000)
	// fwmark-only, so priority 1 stays after "lookup local" and still beats
	// Clash/tun catch-all rules that commonly sit at 100–9000.
	priority = 1
	return
}

func activateExportProxyInterface(ctx context.Context, candidate modem.Candidate, atClient modem.Client) (string, error) {
	// AT+QNETDEVCTL / ECM paths should not prefer leftover QMI WDS settings.
	return bringUpExportProxyInterface(ctx, candidate, atClient, []string{"dhcp", "at-pdp", "qmi-wds"})
}

func bringUpExportProxyInterface(ctx context.Context, candidate modem.Candidate, atClient modem.Client, sources []string) (string, error) {
	networkInterface := strings.TrimSpace(candidate.NetworkInterface)
	if networkInterface == "" {
		return "", nil
	}
	ipCommand, err := exec.LookPath("ip")
	if err != nil {
		return "", fmt.Errorf("%w: install iproute2 to control %s", ErrDataBackendUnavailable, networkInterface)
	}
	if err := ensureQMIRawIP(ctx, ipCommand, networkInterface); err != nil {
		return "", err
	}
	if result, linkErr := exec.CommandContext(ctx, ipCommand, "link", "set", "dev", networkInterface, "up").CombinedOutput(); linkErr != nil {
		return "", wrapCellularData(fmt.Errorf("set %s up: %w: %s", networkInterface, linkErr, strings.TrimSpace(string(result))))
	}
	waitForInterface(ctx, networkInterface, 3*time.Second)
	if err := ensureQMIRawIP(ctx, ipCommand, networkInterface); err != nil {
		return "", err
	}
	if qmiRawIPEnabled(networkInterface) {
		if result, linkErr := exec.CommandContext(ctx, ipCommand, "link", "set", "dev", networkInterface, "up").CombinedOutput(); linkErr != nil {
			return "", wrapCellularData(fmt.Errorf("set %s up after raw_ip: %w: %s", networkInterface, linkErr, strings.TrimSpace(string(result))))
		}
	}
	lease, source, err := obtainCellularLeaseWithRetry(ctx, candidate, atClient, sources, 6*time.Second)
	if err != nil {
		return "", wrapCellularData(err)
	}
	if err := applyCellularLease(ctx, ipCommand, networkInterface, lease); err != nil {
		clearExportProxyRoute(ctx, networkInterface)
		return "", wrapCellularData(err)
	}
	return fmt.Sprintf("protected %s lease %s/%d", source, lease.Address.String(), lease.prefixLen()), nil
}

func deactivateExportProxyInterface(ctx context.Context, networkInterface string) {
	networkInterface = strings.TrimSpace(networkInterface)
	if networkInterface == "" {
		return
	}
	clearExportProxyRoute(ctx, networkInterface)
	ipCommand, err := exec.LookPath("ip")
	if err != nil {
		return
	}
	_, _ = exec.CommandContext(ctx, ipCommand, "-4", "addr", "flush", "dev", networkInterface, "scope", "global").CombinedOutput()
	_, _ = exec.CommandContext(ctx, ipCommand, "link", "set", "dev", networkInterface, "down").CombinedOutput()
}

func obtainCellularLeaseWithRetry(ctx context.Context, candidate modem.Candidate, atClient modem.Client, sources []string, budget time.Duration) (cellularLease, string, error) {
	deadline := time.Now().Add(budget)
	var last error
	for {
		lease, source, err := obtainCellularLease(ctx, candidate, atClient, sources)
		if err == nil {
			return lease, source, nil
		}
		last = err
		if !time.Now().Before(deadline) {
			return cellularLease{}, "", last
		}
		select {
		case <-ctx.Done():
			if last == nil {
				last = ctx.Err()
			}
			return cellularLease{}, "", last
		case <-time.After(800 * time.Millisecond):
		}
	}
}

func obtainCellularLease(ctx context.Context, candidate modem.Candidate, atClient modem.Client, sources []string) (cellularLease, string, error) {
	if len(sources) == 0 {
		sources = []string{"dhcp", "at-pdp", "qmi-wds"}
	}
	var failures []string
	for _, source := range sources {
		var lease cellularLease
		var ok bool
		var detail string
		switch source {
		case "qmi-wds":
			lease, ok, detail = leaseFromQMI(ctx, candidate.QMIControl)
		case "dhcp":
			lease, ok, detail = leaseFromUDHCPC(ctx, candidate.NetworkInterface)
		case "at-pdp":
			lease, ok, detail = leaseFromAT(ctx, atClient)
		default:
			continue
		}
		if ok {
			return lease, source, nil
		}
		if detail != "" {
			failures = append(failures, detail)
		}
	}
	if len(failures) == 0 {
		return cellularLease{}, "", errors.New("no QMI WDS, DHCP, or AT PDP address source is available")
	}
	return cellularLease{}, "", errors.New(strings.Join(failures, "; "))
}

func leaseFromQMI(ctx context.Context, control string) (cellularLease, bool, string) {
	control = strings.TrimSpace(control)
	if control == "" {
		return cellularLease{}, false, ""
	}
	qmicli, err := exec.LookPath("qmicli")
	if err != nil {
		return cellularLease{}, false, "qmicli is not installed"
	}
	output, err := exec.CommandContext(ctx, qmicli, "-d", control, "--device-open-proxy", "--wds-get-current-settings").CombinedOutput()
	detail := strings.TrimSpace(string(output))
	if err != nil {
		if detail == "" {
			detail = err.Error()
		}
		return cellularLease{}, false, "qmicli --wds-get-current-settings: " + detail
	}
	lease, ok := parseQMICurrentSettings(string(output))
	if !ok {
		return cellularLease{}, false, "qmicli returned no IPv4 settings"
	}
	return lease, true, ""
}

func leaseFromUDHCPC(ctx context.Context, networkInterface string) (cellularLease, bool, string) {
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		return cellularLease{}, false, "busybox udhcpc is not installed"
	}
	lease, err := requestUDHCPCLease(ctx, busybox, networkInterface)
	if err != nil {
		return cellularLease{}, false, err.Error()
	}
	return lease, true, ""
}

func leaseFromAT(ctx context.Context, client modem.Client) (cellularLease, bool, string) {
	if client == nil {
		return cellularLease{}, false, ""
	}
	if response, err := client.Execute(ctx, "AT+CGCONTRDP=1"); err == nil && response.OK() {
		if lease, ok := parseCGCONTRDP(response); ok {
			return lease, true, ""
		}
	}
	if response, err := client.Execute(ctx, "AT+CGPADDR=1"); err == nil && response.OK() {
		if lease, ok := parseCGPADDR(response); ok {
			return lease, true, ""
		}
	}
	return cellularLease{}, false, "AT+CGCONTRDP/CGPADDR returned no IPv4 address"
}

func requestUDHCPCLease(ctx context.Context, busybox, networkInterface string) (cellularLease, error) {
	leaseFile, err := os.CreateTemp("", "vocat-dhcp-lease-*.env")
	if err != nil {
		return cellularLease{}, err
	}
	leasePath := leaseFile.Name()
	_ = leaseFile.Close()
	_ = os.Remove(leasePath)
	defer os.Remove(leasePath)
	script, err := os.CreateTemp("", "vocat-udhcpc-*.sh")
	if err != nil {
		return cellularLease{}, err
	}
	scriptPath := script.Name()
	defer os.Remove(scriptPath)
	scriptText := fmt.Sprintf(`#!/bin/sh
case "$1" in
  bound|renew)
    (umask 077; printf 'ip=%%s\nsubnet=%%s\nrouter=%%s\ndns=%%s\n' "$ip" "$subnet" "$router" "$dns" > %q)
    ;;
esac
exit 0
`, leasePath)
	if _, err := script.WriteString(scriptText); err != nil {
		_ = script.Close()
		return cellularLease{}, err
	}
	if err := script.Chmod(0o700); err != nil {
		_ = script.Close()
		return cellularLease{}, err
	}
	if err := script.Close(); err != nil {
		return cellularLease{}, err
	}
	output, err := exec.CommandContext(ctx, busybox, "udhcpc", "-q", "-n", "-t", "5", "-T", "3", "-i", networkInterface, "-s", scriptPath).CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "address family not supported") {
			return cellularLease{}, fmt.Errorf("udhcpc cannot open its link-layer socket: allow AF_PACKET in the vocat systemd service RestrictAddressFamilies setting: %w", err)
		}
		return cellularLease{}, fmt.Errorf("udhcpc: %w: %s", err, strings.TrimSpace(string(output)))
	}
	raw, err := os.ReadFile(leasePath)
	if err != nil {
		return cellularLease{}, fmt.Errorf("read DHCP lease: %w", err)
	}
	values := make(map[string]string)
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	address := net.ParseIP(values["ip"]).To4()
	maskIP := net.ParseIP(values["subnet"]).To4()
	if address == nil || maskIP == nil {
		return cellularLease{}, errors.New("DHCP returned no valid IPv4 address/subnet")
	}
	lease := cellularLease{Address: address, Mask: net.IPMask(maskIP), DNS: strings.Fields(values["dns"])}
	if routers := strings.Fields(values["router"]); len(routers) > 0 {
		if gateway := net.ParseIP(routers[0]).To4(); gateway != nil {
			lease.Gateway = gateway
		} else {
			return cellularLease{}, errors.New("DHCP returned an invalid IPv4 gateway")
		}
	}
	if ones, bits := lease.Mask.Size(); bits != 32 || ones < 0 {
		return cellularLease{}, errors.New("DHCP returned an invalid IPv4 subnet")
	}
	return lease, nil
}

func applyCellularLease(ctx context.Context, ipCommand, networkInterface string, lease cellularLease) error {
	if lease.Address.To4() == nil {
		return errors.New("cellular lease has no IPv4 address")
	}
	if lease.Mask == nil {
		lease.Mask = net.CIDRMask(32, 32)
	}
	ones := lease.prefixLen()
	if result, addrErr := exec.CommandContext(ctx, ipCommand, "-4", "addr", "replace", fmt.Sprintf("%s/%d", lease.Address.String(), ones), "dev", networkInterface).CombinedOutput(); addrErr != nil {
		return fmt.Errorf("configure cellular address: %w: %s", addrErr, strings.TrimSpace(string(result)))
	}
	mark, table, priority := exportProxyRouteIdentity(networkInterface)
	clearExportProxyRoute(ctx, networkInterface)
	network := lease.Address.Mask(lease.Mask)
	connectedCIDR := fmt.Sprintf("%s/%d", network.String(), ones)
	if result, routeErr := exec.CommandContext(ctx, ipCommand, "-4", "route", "replace", "table", strconv.Itoa(table), connectedCIDR, "dev", networkInterface, "scope", "link", "src", lease.Address.String()).CombinedOutput(); routeErr != nil {
		return fmt.Errorf("install protected connected route: %w: %s", routeErr, strings.TrimSpace(string(result)))
	}
	rawIP := qmiRawIPEnabled(networkInterface)
	if rawIP {
		prepareRawIPLink(ctx, ipCommand, networkInterface)
	}
	nextHop := lease.nextHop()
	if rawIP {
		// raw-ip has no Ethernet ARP. "via <peer> onlink" makes TCP connect()
		// fail with EHOSTUNREACH even when `ip route get oif` looks correct.
		nextHop = nil
	}
	if err := installCellularDefaults(ctx, ipCommand, networkInterface, nextHop, table); err != nil {
		return err
	}
	markText := fmt.Sprintf("0x%x", mark)
	ruleArgs := []string{"-4", "rule", "add", "priority", strconv.Itoa(priority), "fwmark", markText, "lookup", strconv.Itoa(table)}
	result, err := exec.CommandContext(ctx, ipCommand, ruleArgs...).CombinedOutput()
	if err != nil && strings.Contains(strings.ToLower(string(result)), "file exists") {
		_, _ = exec.CommandContext(ctx, ipCommand, "-4", "rule", "del", "priority", strconv.Itoa(priority), "fwmark", markText, "lookup", strconv.Itoa(table)).CombinedOutput()
		result, err = exec.CommandContext(ctx, ipCommand, ruleArgs...).CombinedOutput()
	}
	if err != nil {
		return fmt.Errorf("install protected routing rule: %w: %s", err, strings.TrimSpace(string(result)))
	}
	if err := writeExportProxyDNS(networkInterface, lease.DNS); err != nil {
		return fmt.Errorf("publish protected DNS configuration: %w", err)
	}
	if err := verifyMarkedCellularRoute(ctx, ipCommand, networkInterface, lease.Address, markText); err != nil {
		return err
	}
	if err := ensureBoundCellularPath(ctx, ipCommand, networkInterface, nextHop, table, mark); err != nil {
		return err
	}
	return nil
}

func installCellularDefaults(ctx context.Context, ipCommand, networkInterface string, nextHop net.IP, table int) error {
	if err := installCellularDefault(ctx, ipCommand, networkInterface, nextHop, strconv.Itoa(table), ""); err != nil {
		return err
	}
	// BINDTODEVICE can ignore policy tables and only see this interface's main
	// routes. A high-metric default keeps eth0 as the host default.
	return installCellularDefault(ctx, ipCommand, networkInterface, nextHop, "", strconv.Itoa(cellularMainMetric))
}

const cellularMainMetric = 25000

func installCellularDefault(ctx context.Context, ipCommand, networkInterface string, nextHop net.IP, table, metric string) error {
	args := []string{"-4", "route", "replace", "default"}
	if table != "" {
		args = []string{"-4", "route", "replace", "table", table, "default"}
	}
	if nextHop != nil {
		args = append(args, "via", nextHop.String(), "dev", networkInterface, "onlink")
	} else {
		args = append(args, "dev", networkInterface)
	}
	if metric != "" {
		args = append(args, "metric", metric)
	}
	result, err := exec.CommandContext(ctx, ipCommand, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("install cellular default route: %w: %s", err, strings.TrimSpace(string(result)))
	}
	return nil
}

func verifyMarkedCellularRoute(ctx context.Context, ipCommand, networkInterface string, source net.IP, markText string) error {
	args := []string{"-4", "route", "get", "1.1.1.1", "mark", markText, "oif", networkInterface}
	if source != nil {
		args = append(args, "from", source.String())
	}
	result, err := exec.CommandContext(ctx, ipCommand, args...).CombinedOutput()
	detail := strings.TrimSpace(string(result))
	if err != nil {
		return fmt.Errorf("marked sockets still have no route out %s: %w: %s", networkInterface, err, detail)
	}
	if !strings.Contains(detail, "dev "+networkInterface) {
		return fmt.Errorf("marked sockets still have no route out %s: %s", networkInterface, detail)
	}
	return nil
}

func qmiRawIPEnabled(networkInterface string) bool {
	if !safeSysfsInterfaceName(networkInterface) {
		return false
	}
	current, err := os.ReadFile("/sys/class/net/" + networkInterface + "/qmi/raw_ip")
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(string(current)), "Y")
}

func prepareRawIPLink(ctx context.Context, ipCommand, networkInterface string) {
	_, _ = exec.CommandContext(ctx, ipCommand, "link", "set", "dev", networkInterface, "arp", "off").CombinedOutput()
	if safeSysfsInterfaceName(networkInterface) {
		_ = os.WriteFile("/proc/sys/net/ipv4/conf/"+networkInterface+"/rp_filter", []byte("2\n"), 0o644)
	}
}

func ensureBoundCellularPath(ctx context.Context, ipCommand, networkInterface string, nextHop net.IP, table int, mark uint32) error {
	err := probeBoundCellularTCP(ctx, networkInterface, mark)
	if err == nil || !isNoRouteError(err) {
		return nil
	}
	prepareRawIPLink(ctx, ipCommand, networkInterface)
	if nextHop != nil {
		if instErr := installCellularDefaults(ctx, ipCommand, networkInterface, nil, table); instErr != nil {
			return instErr
		}
		if retry := probeBoundCellularTCP(ctx, networkInterface, mark); retry == nil || !isNoRouteError(retry) {
			return nil
		}
	}
	if bindOnly := probeBoundCellularTCP(ctx, networkInterface, 0); bindOnly == nil {
		return fmt.Errorf("bound TCP out %s works without SO_MARK; a higher-priority policy rule is stealing marked sockets: %w", networkInterface, err)
	}
	return fmt.Errorf("bound TCP out %s has no usable route: %w", networkInterface, err)
}

func probeBoundCellularTCP(ctx context.Context, networkInterface string, mark uint32) error {
	dialer := net.Dialer{
		Timeout: 4 * time.Second,
		Control: func(_, _ string, raw syscall.RawConn) error {
			var bindError error
			controlErr := raw.Control(func(fd uintptr) {
				if mark != 0 {
					if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, int(mark)); err != nil {
						bindError = err
						return
					}
				}
				bindError = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, networkInterface)
			})
			if controlErr != nil {
				return controlErr
			}
			return bindError
		},
	}
	var last error
	for _, address := range []string{"1.1.1.1:443", "8.8.8.8:53"} {
		probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		conn, err := dialer.DialContext(probeCtx, "tcp4", address)
		cancel()
		if err == nil {
			_ = conn.Close()
			return nil
		}
		last = err
		if isNoRouteError(err) {
			return err
		}
	}
	return last
}

func isNoRouteError(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && (errno == syscall.EHOSTUNREACH || errno == syscall.ENETUNREACH) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no route to host") || strings.Contains(message, "network is unreachable")
}

func ensureQMIRawIP(ctx context.Context, ipCommand, networkInterface string) error {
	if !safeSysfsInterfaceName(networkInterface) {
		return nil
	}
	path := "/sys/class/net/" + networkInterface + "/qmi/raw_ip"
	current, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(string(current)), "Y") {
		prepareRawIPLink(ctx, ipCommand, networkInterface)
		return nil
	}
	// The kernel ignores writes while the netdev is up, so RX stays at 0
	// (ethernet frames never match the modem's raw IP packets).
	if result, downErr := exec.CommandContext(ctx, ipCommand, "link", "set", "dev", networkInterface, "down").CombinedOutput(); downErr != nil {
		return fmt.Errorf("set %s down to enable qmi raw_ip: %w: %s", networkInterface, downErr, strings.TrimSpace(string(result)))
	}
	if err := os.WriteFile(path, []byte("Y\n"), 0o644); err != nil {
		return fmt.Errorf("write %s qmi/raw_ip: %w", networkInterface, err)
	}
	current, err = os.ReadFile(path)
	if err != nil || !strings.EqualFold(strings.TrimSpace(string(current)), "Y") {
		value := strings.TrimSpace(string(current))
		if err != nil {
			value = err.Error()
		}
		return fmt.Errorf("failed to set %s qmi/raw_ip=Y (now %q); the modem will transmit but never receive", networkInterface, value)
	}
	prepareRawIPLink(ctx, ipCommand, networkInterface)
	return nil
}

func waitForInterface(ctx context.Context, networkInterface string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		iface, err := net.InterfaceByName(networkInterface)
		if err == nil && iface.Flags&net.FlagUp != 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func safeSysfsInterfaceName(value string) bool {
	if value == "" || len(value) > 15 || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func exportProxyDNSPath(networkInterface string) string {
	safeName := strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' {
			return character
		}
		return '_'
	}, networkInterface)
	return "/run/vocat/cellular-" + safeName + ".dns"
}

func writeExportProxyDNS(networkInterface string, servers []string) error {
	valid := make([]string, 0, len(servers))
	for _, server := range servers {
		if address := net.ParseIP(server); address != nil {
			valid = append(valid, address.String())
		}
	}
	valid = appendPublicDNSFallbacks(valid)
	if err := os.MkdirAll("/run/vocat", 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp("/run/vocat", ".cellular-dns-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.WriteString(strings.Join(valid, "\n") + "\n"); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, exportProxyDNSPath(networkInterface))
}

func appendPublicDNSFallbacks(servers []string) []string {
	seen := make(map[string]bool, len(servers)+2)
	valid := make([]string, 0, len(servers)+2)
	for _, server := range servers {
		ip := net.ParseIP(server).To4()
		if ip == nil {
			continue
		}
		value := ip.String()
		if seen[value] {
			continue
		}
		seen[value] = true
		valid = append(valid, value)
	}
	for _, fallback := range []string{"1.1.1.1", "8.8.8.8"} {
		if !seen[fallback] {
			valid = append(valid, fallback)
		}
	}
	return valid
}

func clearExportProxyRoute(ctx context.Context, networkInterface string) {
	_ = os.Remove(exportProxyDNSPath(networkInterface))
	ipCommand, err := exec.LookPath("ip")
	if err != nil {
		return
	}
	mark, table, priority := exportProxyRouteIdentity(networkInterface)
	markText := fmt.Sprintf("0x%x", mark)
	for range 8 {
		if _, err := exec.CommandContext(ctx, ipCommand, "-4", "rule", "del", "fwmark", markText).CombinedOutput(); err != nil {
			break
		}
	}
	_, _ = exec.CommandContext(ctx, ipCommand, "-4", "rule", "del", "priority", strconv.Itoa(priority), "fwmark", markText, "lookup", strconv.Itoa(table)).CombinedOutput()
	// Older builds used priority == table (20000 + hash%10000).
	_, _ = exec.CommandContext(ctx, ipCommand, "-4", "rule", "del", "priority", strconv.Itoa(table), "fwmark", markText, "lookup", strconv.Itoa(table)).CombinedOutput()
	_, _ = exec.CommandContext(ctx, ipCommand, "-4", "route", "flush", "table", strconv.Itoa(table)).CombinedOutput()
	_, _ = exec.CommandContext(ctx, ipCommand, "-4", "route", "del", "default", "dev", networkInterface, "metric", strconv.Itoa(cellularMainMetric)).CombinedOutput()
}

const managerCommandCleanupTimeout = 15 * time.Second
