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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"

	"vocat/internal/fwmark"
	"vocat/internal/modem"
)

const (
	qmiDataOpenTimeout   = 15 * time.Second
	qmiDataStatusTimeout = 12 * time.Second
	qmiDataStopTimeout   = 20 * time.Second
	qmiDataStartTimeout  = 60 * time.Second
	qmiDataEventTimeout  = 5 * time.Second
)

type productionQMIDataSession struct {
	client                *qmi.Client
	wds                   *qmi.WDSService
	packetStatusEvents    chan struct{}
	packetStatusEventOnce sync.Once
}

func openQMIDataSession(ctx context.Context, controlDevice string) (qmiDataSession, error) {
	openContext, cancel := context.WithTimeout(ctx, qmiDataOpenTimeout)
	defer cancel()
	opts := qmi.DefaultClientOptions()
	opts.UseProxy = true
	opts.ProxyFallbackToRaw = false
	opts.ProxyExecutable = qmiProxyExecutable()
	opts.Logf = func(qmi.ClientLogLevel, string, ...any) {}
	client, err := qmi.NewClientWithOptions(openContext, controlDevice, opts)
	if err != nil {
		return nil, fmt.Errorf("open QMI WDS transport: %w", err)
	}
	wds, err := qmi.NewWDSServiceWithContext(openContext, client)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("allocate QMI WDS client: %w", err)
	}
	return &productionQMIDataSession{
		client:             client,
		wds:                wds,
		packetStatusEvents: make(chan struct{}, 1),
	}, nil
}

func qmiProxyExecutable() string {
	if path, err := exec.LookPath("qmi-proxy"); err == nil {
		return path
	}
	for _, path := range []string{"/usr/libexec/qmi-proxy", "/usr/lib/qmi-proxy", "/usr/lib/libqmi-glib/qmi-proxy"} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path
		}
	}
	// Keep the library's conventional path in the resulting diagnostic.
	return "/usr/libexec/qmi-proxy"
}

// enableQMIDataEvents is best-effort. Some modem firmware accepts WDS event
// registration but never emits indications, while other firmware rejects the
// request entirely. The periodic NetworkStatus monitor remains authoritative
// in both cases.
func (manager *Manager) enableQMIDataEvents(ctx context.Context, state *managedDevice, deviceID string, session qmiDataSession) {
	source, ok := session.(qmiDataEventSource)
	if !ok {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	registrationContext, cancelRegistration := context.WithTimeout(ctx, qmiDataEventTimeout)
	err := source.RegisterPacketStatusEvents(registrationContext)
	cancelRegistration()
	if err != nil {
		if manager.logger != nil {
			manager.logger.Warn("QMI packet status event registration failed; periodic monitor remains active", "device_id", deviceID, "error", err)
		}
		return
	}
	events := source.PacketStatusEvents()
	if events == nil {
		return
	}
	if manager.logger != nil {
		manager.logger.Info("registered QMI packet status events", "device_id", deviceID)
	}
	if state.dataEventCancel != nil {
		state.dataEventCancel()
	}
	watchContext, cancelWatch := context.WithCancel(context.Background())
	state.dataEventCancel = cancelWatch
	go func() {
		for {
			select {
			case <-watchContext.Done():
				return
			case _, ok := <-events:
				if !ok {
					// A closed event stream means the QMI client disappeared;
					// wake the reconciler so it can confirm the live state now.
					manager.publishNetworkStatusEvent(deviceID)
					return
				}
				manager.publishNetworkStatusEvent(deviceID)
			}
		}
	}()
}

func disableQMIDataEvents(state *managedDevice) {
	if state == nil || state.dataEventCancel == nil {
		return
	}
	state.dataEventCancel()
	state.dataEventCancel = nil
}

func (session *productionQMIDataSession) Start(ctx context.Context, apn, username, password string, authType, ipFamily uint8) (uint32, error) {
	return session.wds.StartNetworkInterface(ctx, apn, username, password, authType, ipFamily)
}

func (session *productionQMIDataSession) Stop(ctx context.Context, handle uint32) error {
	return session.wds.StopNetworkInterface(ctx, handle)
}

func (session *productionQMIDataSession) StopAny(ctx context.Context, disableAutoconnect bool) error {
	return session.wds.StopAnyNetworkInterface(ctx, disableAutoconnect)
}

func (session *productionQMIDataSession) RegisterPacketStatusEvents(ctx context.Context) error {
	if session == nil || session.wds == nil || session.client == nil {
		return errors.New("QMI data session is closed")
	}
	if err := session.wds.RegisterEventReport(ctx); err != nil {
		return err
	}
	session.packetStatusEventOnce.Do(func() {
		go func() {
			defer close(session.packetStatusEvents)
			for event := range session.client.Events() {
				if event.Type != qmi.EventPacketServiceStatusChanged {
					continue
				}
				select {
				case session.packetStatusEvents <- struct{}{}:
				default:
					// Coalesce bursts; the consumer always performs a fresh
					// NetworkStatus query before deciding whether to recover.
				}
			}
		}()
	})
	return nil
}

func (session *productionQMIDataSession) PacketStatusEvents() <-chan struct{} {
	if session == nil {
		return nil
	}
	return session.packetStatusEvents
}

func (session *productionQMIDataSession) Connected(ctx context.Context) (bool, error) {
	status, err := session.wds.GetPacketServiceStatus(ctx)
	return status == qmi.StatusConnected || status == qmi.StatusSuspended, err
}

func (session *productionQMIDataSession) RawIP(ctx context.Context) (bool, error) {
	wda, err := qmi.NewWDAServiceWithContext(ctx, session.client)
	if err != nil {
		return false, err
	}
	defer wda.Close()
	format, err := wda.GetDataFormat(ctx)
	if err != nil {
		return false, err
	}
	return format.LinkProtocol == qmi.LinkProtocolIP, nil
}

func (session *productionQMIDataSession) SetRawIP(ctx context.Context) error {
	wda, err := qmi.NewWDAServiceWithContext(ctx, session.client)
	if err != nil {
		return err
	}
	defer wda.Close()
	return wda.SetDataFormat(ctx, qmi.DataFormat{
		LinkProtocol:      qmi.LinkProtocolIP,
		UlDataAggregation: uint32(qmi.DataFormatUlDataAggDisabled),
		DlDataAggregation: uint32(qmi.DataFormatDlDataAggDisabled),
	})
}

func (session *productionQMIDataSession) RuntimeIPv4(ctx context.Context) (qmiIPv4Settings, error) {
	settings, err := session.wds.GetRuntimeSettings(ctx, 4)
	if err != nil {
		return qmiIPv4Settings{}, err
	}
	address := settings.IPv4Address.To4()
	ones, bits := settings.IPv4Subnet.Size()
	if address == nil || address.IsUnspecified() || bits != 32 || ones < 0 {
		return qmiIPv4Settings{}, errors.New("QMI WDS returned no valid IPv4 address/subnet")
	}
	result := qmiIPv4Settings{Address: address.String(), Prefix: ones, MTU: settings.MTU}
	if gateway := settings.IPv4Gateway.To4(); gateway != nil && !gateway.IsUnspecified() {
		result.Gateway = gateway.String()
	}
	for _, server := range []net.IP{settings.IPv4DNS1, settings.IPv4DNS2} {
		if server = server.To4(); server != nil && !server.IsUnspecified() {
			result.DNS = append(result.DNS, server.String())
		}
	}
	return result, nil
}

func (session *productionQMIDataSession) Close() error {
	if session == nil {
		return nil
	}
	var closeErrors []error
	if session.wds != nil {
		closeErrors = append(closeErrors, session.wds.Close())
		session.wds = nil
	}
	if session.client != nil {
		closeErrors = append(closeErrors, session.client.Close())
		session.client = nil
	}
	return errors.Join(closeErrors...)
}

// NetworkStatus observes the current QMI call instead of inferring it from the
// last SetNetwork result. TryLock keeps an overview refresh from waiting behind
// a long start/stop transaction; callers can retain the in-progress phase in
// that case.
func (manager *Manager) NetworkStatus(ctx context.Context, id string) (NetworkStatus, error) {
	state, err := manager.lookup(id)
	if err != nil {
		return NetworkStatus{}, err
	}
	if !state.dataMu.TryLock() {
		return NetworkStatus{}, ErrDataOperationInProgress
	}
	defer state.dataMu.Unlock()
	if err := manager.validateActive(id, state); err != nil {
		return NetworkStatus{}, err
	}
	candidate := manager.candidateFor(state)
	status := NetworkStatus{Backend: "qmi", Interface: candidate.NetworkInterface}
	if candidate.QMIControl == "" || candidate.NetworkInterface == "" {
		return status, ErrDataBackendUnavailable
	}
	if state.dataSession == nil || state.dataSessionControl != candidate.QMIControl {
		status.Detail = "no process-owned QMI data session"
		return status, nil
	}
	statusContext, cancel := context.WithTimeout(ctx, qmiDataStatusTimeout)
	connected, err := state.dataSession.Connected(statusContext)
	cancel()
	if err != nil {
		return status, fmt.Errorf("query QMI packet service status: %w", err)
	}
	if !connected {
		status.Detail = "QMI packet data session is disconnected"
		return status, nil
	}
	rawContext, cancelRaw := context.WithTimeout(ctx, qmiDataStatusTimeout)
	modemRaw, rawErr := state.dataSession.RawIP(rawContext)
	cancelRaw()
	if rawErr != nil {
		return status, fmt.Errorf("query QMI data format: %w", rawErr)
	}
	if err := validateQMIDataFormat(candidate.NetworkInterface, modemRaw); err != nil {
		status.Detail = err.Error()
		return status, nil
	}
	if err := qmiDataHostReady(ctx, candidate.NetworkInterface); err != nil {
		status.Detail = err.Error()
		return status, nil
	}
	status.Connected = true
	status.Detail = "QMI packet data session and host route are ready"
	return status, nil
}

func qmiRawIPSysfsPath(networkInterface string) (string, error) {
	clean := strings.TrimSpace(networkInterface)
	if clean == "" || filepath.Base(clean) != clean || clean == "." {
		return "", fmt.Errorf("invalid cellular interface %q", networkInterface)
	}
	return filepath.Join("/sys/class/net", clean, "qmi", "raw_ip"), nil
}

func readKernelRawIP(path string) (supported, enabled bool, err error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	value := strings.TrimSpace(string(content))
	return true, strings.EqualFold(value, "Y") || value == "1", nil
}

func validateQMIDataFormat(networkInterface string, modemRaw bool) error {
	path, err := qmiRawIPSysfsPath(networkInterface)
	if err != nil {
		return err
	}
	supported, kernelRaw, err := readKernelRawIP(path)
	if err != nil {
		return fmt.Errorf("read kernel QMI data format: %w", err)
	}
	if !supported {
		if modemRaw {
			return errors.New("modem uses Raw-IP but the kernel interface cannot enable Raw-IP")
		}
		return nil
	}
	if modemRaw != kernelRaw {
		return fmt.Errorf("QMI data format mismatch: modem raw_ip=%t, kernel raw_ip=%t", modemRaw, kernelRaw)
	}
	return nil
}

// prepareQMIDataFormat restores the link-layer setup formerly performed by
// qmi-network. qmi_wwan exposes a raw_ip switch; when present, both WDA and the
// kernel must use Raw-IP or the host emits ARP frames which never enter WDS.
func prepareQMIDataFormat(
	ctx context.Context,
	session qmiDataSession,
	ipCommand string,
	networkInterface string,
	rawIPPath string,
) error {
	supported, kernelRaw, err := readKernelRawIP(rawIPPath)
	if err != nil {
		return fmt.Errorf("read kernel QMI data format: %w", err)
	}
	formatContext, cancelFormat := context.WithTimeout(ctx, qmiDataStatusTimeout)
	modemRaw, err := session.RawIP(formatContext)
	cancelFormat()
	if err != nil {
		return fmt.Errorf("query modem QMI data format: %w", err)
	}
	if !supported {
		if modemRaw {
			return errors.New("modem uses Raw-IP but the kernel interface cannot enable Raw-IP")
		}
		return nil
	}
	if kernelRaw && modemRaw {
		return nil
	}

	// Changing the framing of a live interface is unsafe. Remove every host
	// path first so a partial failure remains fail-closed.
	clearExportProxyRoute(ctx, networkInterface)
	_, _ = exec.CommandContext(ctx, ipCommand, "-4", "addr", "flush", "dev", networkInterface, "scope", "global").CombinedOutput()
	downOutput, downErr := exec.CommandContext(ctx, ipCommand, "link", "set", "dev", networkInterface, "down").CombinedOutput()
	if downErr != nil {
		return fmt.Errorf("set %s down before QMI data-format change: %w: %s", networkInterface, downErr, strings.TrimSpace(string(downOutput)))
	}
	if !modemRaw {
		setContext, cancelSet := context.WithTimeout(ctx, qmiDataStatusTimeout)
		err = session.SetRawIP(setContext)
		cancelSet()
		if err != nil {
			return fmt.Errorf("set modem QMI data format to Raw-IP: %w", err)
		}
	}
	if !kernelRaw {
		if err := os.WriteFile(rawIPPath, []byte("Y\n"), 0o644); err != nil {
			return fmt.Errorf("set kernel QMI data format to Raw-IP: %w%s", err, readOnlySysfsHint(err))
		}
	}
	supported, kernelRaw, err = readKernelRawIP(rawIPPath)
	if err != nil {
		return fmt.Errorf("kernel QMI Raw-IP verification failed: %w%s", err, readOnlySysfsHint(err))
	}
	if !supported || !kernelRaw {
		return fmt.Errorf("kernel QMI Raw-IP verification failed: supported=%t enabled=%t", supported, kernelRaw)
	}
	verifyContext, cancelVerify := context.WithTimeout(ctx, qmiDataStatusTimeout)
	modemRaw, err = session.RawIP(verifyContext)
	cancelVerify()
	if err != nil {
		return fmt.Errorf("modem QMI Raw-IP verification failed: enabled=%t: %w", modemRaw, err)
	}
	if !modemRaw {
		return fmt.Errorf("modem QMI Raw-IP verification failed: enabled=%t", modemRaw)
	}
	prepareRawIPLink(ctx, ipCommand, networkInterface)
	return nil
}

func qmiDataHostReady(ctx context.Context, networkInterface string) error {
	interfaceInfo, err := net.InterfaceByName(networkInterface)
	if err != nil {
		return fmt.Errorf("cellular interface is unavailable: %w", err)
	}
	addresses, err := interfaceInfo.Addrs()
	if err != nil {
		return fmt.Errorf("read cellular interface addresses: %w", err)
	}
	hasIPv4 := false
	for _, value := range addresses {
		address, _, parseErr := net.ParseCIDR(value.String())
		if parseErr == nil && address.To4() != nil && !address.IsUnspecified() {
			hasIPv4 = true
			break
		}
	}
	if !hasIPv4 {
		return errors.New("cellular interface has no IPv4 address")
	}
	ipCommand, err := exec.LookPath("ip")
	if err != nil {
		return fmt.Errorf("check cellular policy route: %w", err)
	}
	_, table, _ := exportProxyRouteIdentity(networkInterface)
	output, err := exec.CommandContext(ctx, ipCommand, "-4", "route", "show", "table", strconv.Itoa(table), "default", "dev", networkInterface).CombinedOutput()
	if err != nil {
		return fmt.Errorf("check cellular policy route: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if strings.TrimSpace(string(output)) == "" {
		return errors.New("cellular policy route is missing")
	}
	return nil
}

func legacyQMINetworkStatePath(controlDevice string) string {
	name := filepath.Base(filepath.Clean(controlDevice))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return ""
	}
	return filepath.Join(os.TempDir(), "qmi-network-state-"+name)
}

func removeLegacyQMINetworkState(candidate modem.Candidate) {
	if path := legacyQMINetworkStatePath(candidate.QMIControl); path != "" {
		_ = os.Remove(path)
	}
}

// invalidateQMINetworkSession is called immediately before a radio/SIM reset.
// Closing the transport forgets the old WDS client and handle without waiting
// for a modem transaction which the reset is about to invalidate anyway.
func invalidateQMINetworkSession(state *managedDevice, candidate modem.Candidate) {
	removeLegacyQMINetworkState(candidate)
	if state == nil {
		return
	}
	disableQMIDataEvents(state)
	if state.dataSession == nil {
		return
	}
	_ = state.dataSession.Close()
	state.dataSession = nil
	state.dataSessionHandle = 0
	state.dataSessionControl = ""
}

func stoppedQMIDataError(err error) bool {
	if err == nil {
		return true
	}
	var outOfCall *qmi.OutOfCallError
	if errors.As(err, &outOfCall) {
		return true
	}
	protocolError := qmi.GetQMIError(err)
	if protocolError == nil {
		return false
	}
	switch protocolError.ErrorCode {
	case qmi.QMIErrInvalidID, qmi.QMIErrNoEffect, qmi.QMIErrOutOfCall:
		return true
	default:
		return false
	}
}

func qmiDataIPFamily(ipVersion string) uint8 {
	if ipVersion == "IPV6" {
		return 6
	}
	// Preserve the existing IPV4V6 behaviour: VoCat currently configures and
	// policy-routes an IPv4 DHCP lease for the Export Proxy.
	return 4
}

func closeQMIDataSession(state *managedDevice) {
	disableQMIDataEvents(state)
	if state.dataSession != nil {
		_ = state.dataSession.Close()
	}
	state.dataSession = nil
	state.dataSessionHandle = 0
	state.dataSessionControl = ""
}

func rollbackQMIDataHost(state *managedDevice, candidate modem.Candidate, ipCommand string) {
	rollbackContext, cancel := context.WithTimeout(context.Background(), managerCommandCleanupTimeout)
	defer cancel()
	clearExportProxyRoute(rollbackContext, candidate.NetworkInterface)
	if state.dataSession != nil {
		_ = state.dataSession.Stop(rollbackContext, state.dataSessionHandle)
	}
	closeQMIDataSession(state)
	_, _ = exec.CommandContext(rollbackContext, ipCommand, "-4", "addr", "flush", "dev", candidate.NetworkInterface, "scope", "global").CombinedOutput()
	_, _ = exec.CommandContext(rollbackContext, ipCommand, "link", "set", "dev", candidate.NetworkInterface, "down").CombinedOutput()
}

func (manager *Manager) stopQMIDataSession(ctx context.Context, state *managedDevice, candidate modem.Candidate) error {
	removeLegacyQMINetworkState(candidate)
	if state.dataSession != nil && state.dataSessionControl == candidate.QMIControl {
		stopContext, cancel := context.WithTimeout(ctx, qmiDataStopTimeout)
		err := state.dataSession.Stop(stopContext, state.dataSessionHandle)
		cancel()
		closeQMIDataSession(state)
		if stoppedQMIDataError(err) {
			return nil
		}
		return err
	}
	closeQMIDataSession(state)
	openContext, cancelOpen := context.WithTimeout(ctx, qmiDataOpenTimeout)
	session, err := manager.qmiDataOpener(openContext, candidate.QMIControl)
	cancelOpen()
	if err != nil {
		return err
	}
	defer session.Close()
	stopContext, cancelStop := context.WithTimeout(ctx, qmiDataStopTimeout)
	err = session.StopAny(stopContext, true)
	cancelStop()
	if stoppedQMIDataError(err) {
		return nil
	}
	return err
}

func (manager *Manager) setQMINetwork(
	ctx context.Context,
	state *managedDevice,
	candidate modem.Candidate,
	enabled bool,
	apn string,
	ipVersion string,
	username string,
	password string,
	authentication string,
) (NetworkResult, error) {
	ipCommand, err := exec.LookPath("ip")
	if err != nil {
		return NetworkResult{}, fmt.Errorf("%w: install iproute2 to control %s", ErrDataBackendUnavailable, candidate.NetworkInterface)
	}

	// Disabling is fail-closed: remove every host path before asking the modem
	// to release WDS. Even if the baseband session is stale or unresponsive, no
	// application can continue routing traffic through the cellular interface.
	if !enabled {
		cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
		clearExportProxyRoute(cleanupContext, candidate.NetworkInterface)
		_, _ = exec.CommandContext(cleanupContext, ipCommand, "-4", "addr", "flush", "dev", candidate.NetworkInterface, "scope", "global").CombinedOutput()
		_, _ = exec.CommandContext(cleanupContext, ipCommand, "link", "set", "dev", candidate.NetworkInterface, "down").CombinedOutput()
		cancelCleanup()
		err := manager.stopQMIDataSession(ctx, state, candidate)
		if err != nil {
			return NetworkResult{}, fmt.Errorf("stop QMI data session: %w", err)
		}
		return NetworkResult{
			Enabled: false, Backend: "qmi", Interface: candidate.NetworkInterface,
			ControlDevice: candidate.QMIControl, APN: apn, IPVersion: ipVersion,
			Detail: "packet data session stopped",
		}, nil
	}

	removeLegacyQMINetworkState(candidate)
	if state.dataSession != nil && state.dataSessionControl == candidate.QMIControl {
		rawIPPath, pathErr := qmiRawIPSysfsPath(candidate.NetworkInterface)
		if pathErr != nil {
			return NetworkResult{}, pathErr
		}
		if formatErr := prepareQMIDataFormat(ctx, state.dataSession, ipCommand, candidate.NetworkInterface, rawIPPath); formatErr != nil {
			rollbackQMIDataHost(state, candidate, ipCommand)
			return NetworkResult{}, fmt.Errorf("prepare QMI data format: %w", formatErr)
		}
		statusContext, cancel := context.WithTimeout(ctx, qmiDataStatusTimeout)
		connected, statusErr := state.dataSession.Connected(statusContext)
		cancel()
		if statusErr == nil && connected {
			return configureQMIDataHost(ctx, state, candidate, ipCommand, apn, ipVersion, "packet data session already connected")
		}
		closeQMIDataSession(state)
	}

	// A process restart loses the old WDS handle. StopAnyNetworkInterface is a
	// modem-side recovery primitive, so stale sessions are reclaimed without
	// trusting a CID/PDH cached under a reused cdc-wdm basename.
	openContext, cancelOpen := context.WithTimeout(ctx, qmiDataOpenTimeout)
	session, err := manager.qmiDataOpener(openContext, candidate.QMIControl)
	cancelOpen()
	if err != nil {
		return NetworkResult{}, fmt.Errorf("open QMI data session: %w", err)
	}
	manager.enableQMIDataEvents(ctx, state, candidate.ID, session)
	stopContext, cancelStop := context.WithTimeout(ctx, qmiDataStopTimeout)
	stopErr := session.StopAny(stopContext, true)
	cancelStop()
	if !stoppedQMIDataError(stopErr) {
		disableQMIDataEvents(state)
		_ = session.Close()
		return NetworkResult{}, fmt.Errorf("reclaim stale QMI data session: %w", stopErr)
	}
	rawIPPath, pathErr := qmiRawIPSysfsPath(candidate.NetworkInterface)
	if pathErr != nil {
		disableQMIDataEvents(state)
		_ = session.Close()
		return NetworkResult{}, pathErr
	}
	if formatErr := prepareQMIDataFormat(ctx, session, ipCommand, candidate.NetworkInterface, rawIPPath); formatErr != nil {
		disableQMIDataEvents(state)
		_ = session.Close()
		return NetworkResult{}, fmt.Errorf("prepare QMI data format: %w", formatErr)
	}
	authType := map[string]uint8{"NONE": 0, "PAP": 1, "CHAP": 2, "PAP_OR_CHAP": 3}[authentication]
	startContext, cancelStart := context.WithTimeout(ctx, qmiDataStartTimeout)
	handle, err := session.Start(startContext, apn, username, password, authType, qmiDataIPFamily(ipVersion))
	cancelStart()
	if err != nil {
		disableQMIDataEvents(state)
		_ = session.Close()
		return NetworkResult{}, fmt.Errorf("start QMI data session: %w", err)
	}
	state.dataSession = session
	state.dataSessionHandle = handle
	state.dataSessionControl = candidate.QMIControl
	return configureQMIDataHost(ctx, state, candidate, ipCommand, apn, ipVersion, fmt.Sprintf("packet data handle %d", handle))
}

func configureQMIDataHost(ctx context.Context, state *managedDevice, candidate modem.Candidate, ipCommand, apn, ipVersion, detail string) (NetworkResult, error) {
	// The modem-side call and the host-side lease have independent lifetimes.
	// A daemon/modem restart can leave an address from the previous packet call
	// on the interface, so DHCP must always begin from a clean host generation.
	clearExportProxyRoute(ctx, candidate.NetworkInterface)
	flushOutput, flushErr := exec.CommandContext(ctx, ipCommand, "-4", "addr", "flush", "dev", candidate.NetworkInterface, "scope", "global").CombinedOutput()
	if flushErr != nil {
		rollbackQMIDataHost(state, candidate, ipCommand)
		return NetworkResult{}, fmt.Errorf("clear stale addresses on %s: %w: %s", candidate.NetworkInterface, flushErr, strings.TrimSpace(string(flushOutput)))
	}
	linkOutput, linkErr := exec.CommandContext(ctx, ipCommand, "link", "set", "dev", candidate.NetworkInterface, "up").CombinedOutput()
	if linkErr != nil {
		rollbackQMIDataHost(state, candidate, ipCommand)
		return NetworkResult{}, fmt.Errorf("set %s up: %w: %s", candidate.NetworkInterface, linkErr, strings.TrimSpace(string(linkOutput)))
	}
	runtimeContext, cancelRuntime := context.WithTimeout(ctx, qmiDataStatusTimeout)
	runtimeSettings, runtimeErr := state.dataSession.RuntimeIPv4(runtimeContext)
	cancelRuntime()
	if runtimeErr == nil {
		if runtimeSettings.MTU >= 576 && runtimeSettings.MTU <= 65535 {
			mtuOutput, mtuErr := exec.CommandContext(ctx, ipCommand, "link", "set", "dev", candidate.NetworkInterface, "mtu", strconv.Itoa(runtimeSettings.MTU)).CombinedOutput()
			if mtuErr != nil {
				rollbackQMIDataHost(state, candidate, ipCommand)
				return NetworkResult{}, fmt.Errorf("set %s MTU: %w: %s", candidate.NetworkInterface, mtuErr, strings.TrimSpace(string(mtuOutput)))
			}
		}
		runtimeDetail, configureErr := configureExportProxyIPv4(
			ctx, ipCommand, candidate.NetworkInterface,
			runtimeSettings.Address, runtimeSettings.Prefix,
			runtimeSettings.Gateway, runtimeSettings.DNS,
		)
		if configureErr != nil {
			rollbackQMIDataHost(state, candidate, ipCommand)
			return NetworkResult{}, fmt.Errorf("QMI session started but WDS runtime configuration failed: %w", configureErr)
		}
		return NetworkResult{
			Enabled: true, Backend: "qmi", Interface: candidate.NetworkInterface,
			ControlDevice: candidate.QMIControl, APN: apn, IPVersion: ipVersion,
			Detail: strings.TrimSpace(detail + "\n" + runtimeDetail),
		}, nil
	}
	busybox, err := exec.LookPath("busybox")
	if err != nil {
		rollbackQMIDataHost(state, candidate, ipCommand)
		return NetworkResult{}, fmt.Errorf("%w: WDS runtime settings unavailable (%v) and busybox udhcpc is required for %s", ErrDataBackendUnavailable, runtimeErr, candidate.NetworkInterface)
	}
	dhcpDetail, err := configureExportProxyDHCP(ctx, busybox, ipCommand, candidate.NetworkInterface)
	if err != nil {
		rollbackQMIDataHost(state, candidate, ipCommand)
		return NetworkResult{}, fmt.Errorf("QMI session started but WDS runtime settings were unavailable (%v) and protected DHCP failed: %w", runtimeErr, err)
	}
	return NetworkResult{
		Enabled:       true,
		Backend:       "qmi",
		Interface:     candidate.NetworkInterface,
		ControlDevice: candidate.QMIControl,
		APN:           apn,
		IPVersion:     ipVersion,
		Detail:        strings.TrimSpace(detail + "\n" + dhcpDetail),
	}, nil
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

func configureExportProxyDHCP(ctx context.Context, busybox, ipCommand, networkInterface string) (string, error) {
	lease, err := os.CreateTemp("", "vocat-dhcp-lease-*.env")
	if err != nil {
		return "", err
	}
	leasePath := lease.Name()
	_ = lease.Close()
	_ = os.Remove(leasePath)
	defer os.Remove(leasePath)
	script, err := os.CreateTemp("", "vocat-udhcpc-*.sh")
	if err != nil {
		return "", err
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
		return "", err
	}
	if err := script.Chmod(0o700); err != nil {
		_ = script.Close()
		return "", err
	}
	if err := script.Close(); err != nil {
		return "", err
	}
	output, err := exec.CommandContext(ctx, busybox, "udhcpc", "-q", "-n", "-t", "5", "-T", "3", "-i", networkInterface, "-s", scriptPath).CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "address family not supported") {
			return "", fmt.Errorf("udhcpc cannot open its link-layer socket: allow AF_PACKET in the vocat systemd service RestrictAddressFamilies setting: %w", err)
		}
		return "", fmt.Errorf("udhcpc: %w: %s", err, strings.TrimSpace(string(output)))
	}
	raw, err := os.ReadFile(leasePath)
	if err != nil {
		return "", fmt.Errorf("read DHCP lease: %w", err)
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
		return "", errors.New("DHCP returned no valid IPv4 address/subnet")
	}
	mask := net.IPMask(maskIP)
	ones, bits := mask.Size()
	if bits != 32 || ones < 0 {
		return "", errors.New("DHCP returned an invalid IPv4 subnet")
	}
	routers := strings.Fields(values["router"])
	if len(routers) > 0 && net.ParseIP(routers[0]).To4() == nil {
		return "", errors.New("DHCP returned an invalid IPv4 gateway")
	}
	gateway := ""
	if len(routers) > 0 {
		gateway = routers[0]
	}
	return configureExportProxyIPv4(ctx, ipCommand, networkInterface, address.String(), ones, gateway, strings.Fields(values["dns"]))
}

func configureExportProxyIPv4(ctx context.Context, ipCommand, networkInterface, addressText string, prefix int, gatewayText string, dns []string) (string, error) {
	address := net.ParseIP(addressText).To4()
	if address == nil || prefix < 0 || prefix > 32 {
		return "", errors.New("no valid IPv4 address/subnet was provided")
	}
	mask := net.CIDRMask(prefix, 32)
	network := address.Mask(mask)
	gateway := net.ParseIP(gatewayText).To4()
	if gatewayText != "" && gateway == nil {
		return "", errors.New("an invalid IPv4 gateway was provided")
	}
	if result, addrErr := exec.CommandContext(ctx, ipCommand, "-4", "addr", "replace", fmt.Sprintf("%s/%d", address.String(), prefix), "dev", networkInterface).CombinedOutput(); addrErr != nil {
		return "", fmt.Errorf("configure cellular address: %w: %s", addrErr, strings.TrimSpace(string(result)))
	}
	mark, table, priority := exportProxyRouteIdentity(networkInterface)
	clearExportProxyRoute(ctx, networkInterface)
	connectedCIDR := fmt.Sprintf("%s/%d", network.String(), prefix)
	if result, routeErr := exec.CommandContext(ctx, ipCommand, "-4", "route", "replace", "table", strconv.Itoa(table), connectedCIDR, "dev", networkInterface, "scope", "link", "src", address.String()).CombinedOutput(); routeErr != nil {
		clearExportProxyRoute(ctx, networkInterface)
		return "", fmt.Errorf("install protected connected route: %w: %s", routeErr, strings.TrimSpace(string(result)))
	}
	rawIP := qmiRawIPEnabled(networkInterface)
	if rawIP {
		prepareRawIPLink(ctx, ipCommand, networkInterface)
	}
	nextHop := gateway
	if rawIP {
		// raw-ip has no Ethernet ARP. "via <peer> onlink" makes TCP connect()
		// fail with EHOSTUNREACH even when `ip route get oif` looks correct.
		nextHop = nil
	}
	if err := installCellularDefaults(ctx, ipCommand, networkInterface, nextHop, table); err != nil {
		clearExportProxyRoute(ctx, networkInterface)
		return "", err
	}
	markText := fmt.Sprintf("0x%x", mark)
	ruleArgs := []string{"-4", "rule", "add", "priority", strconv.Itoa(priority), "fwmark", markText, "lookup", strconv.Itoa(table)}
	result, err := exec.CommandContext(ctx, ipCommand, ruleArgs...).CombinedOutput()
	if err != nil && strings.Contains(strings.ToLower(string(result)), "file exists") {
		_, _ = exec.CommandContext(ctx, ipCommand, "-4", "rule", "del", "priority", strconv.Itoa(priority), "fwmark", markText, "lookup", strconv.Itoa(table)).CombinedOutput()
		result, err = exec.CommandContext(ctx, ipCommand, ruleArgs...).CombinedOutput()
	}
	if err != nil {
		clearExportProxyRoute(ctx, networkInterface)
		return "", fmt.Errorf("install protected routing rule: %w: %s", err, strings.TrimSpace(string(result)))
	}
	fwmark.EnsureDNSBypass(mark)
	if err := writeExportProxyDNS(networkInterface, dns); err != nil {
		clearExportProxyRoute(ctx, networkInterface)
		return "", fmt.Errorf("publish protected DNS configuration: %w", err)
	}
	if err := verifyMarkedCellularRoute(ctx, ipCommand, networkInterface, address, markText); err != nil {
		clearExportProxyRoute(ctx, networkInterface)
		return "", err
	}
	if err := ensureBoundCellularPath(ctx, ipCommand, networkInterface, nextHop, table, mark); err != nil {
		clearExportProxyRoute(ctx, networkInterface)
		return "", err
	}
	return fmt.Sprintf("protected IPv4 configuration %s/%d", address.String(), prefix), nil
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
	valid := appendPublicDNSFallbacks(servers)
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

const (
	managerCommandCleanupTimeout = 15 * time.Second
	cellularMainMetric           = 25000
)

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

func qmiWWANRawIP(networkInterface string) bool {
	path, err := qmiRawIPSysfsPath(networkInterface)
	if err != nil {
		return false
	}
	supported, _, err := readKernelRawIP(path)
	return err == nil && supported
}

func qmiRawIPEnabled(networkInterface string) bool {
	path, err := qmiRawIPSysfsPath(networkInterface)
	if err != nil {
		return false
	}
	_, enabled, err := readKernelRawIP(path)
	return err == nil && enabled
}

func prepareRawIPLink(ctx context.Context, ipCommand, networkInterface string) {
	_, _ = exec.CommandContext(ctx, ipCommand, "link", "set", "dev", networkInterface, "arp", "off").CombinedOutput()
	if _, err := qmiRawIPSysfsPath(networkInterface); err == nil {
		_ = os.WriteFile("/proc/sys/net/ipv4/conf/"+networkInterface+"/rp_filter", []byte("2\n"), 0o644)
	}
}

func installCellularDefaults(ctx context.Context, ipCommand, networkInterface string, nextHop net.IP, table int) error {
	if err := installCellularDefault(ctx, ipCommand, networkInterface, nextHop, strconv.Itoa(table), ""); err != nil {
		return err
	}
	// BINDTODEVICE can ignore policy tables and only see this interface's main
	// routes. A high-metric default keeps eth0 as the host default.
	return installCellularDefault(ctx, ipCommand, networkInterface, nextHop, "", strconv.Itoa(cellularMainMetric))
}

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

func readOnlySysfsHint(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, syscall.EROFS) || strings.Contains(strings.ToLower(err.Error()), "read-only file system") {
		return "; /sys is read-only (disable systemd ProtectKernelTunables or mount /sys read-write)"
	}
	return ""
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

func ensureQMIRawIP(ctx context.Context, ipCommand, networkInterface string) error {
	path, err := qmiRawIPSysfsPath(networkInterface)
	if err != nil {
		return nil
	}
	supported, enabled, err := readKernelRawIP(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || !supported {
			return nil
		}
		return fmt.Errorf("write %s qmi/raw_ip: %w%s", networkInterface, err, readOnlySysfsHint(err))
	}
	if !supported {
		return nil
	}
	// Always take the netdev down before writing. A stale Y left while the
	// interface was up is ignored by qmi_wwan: TX increments, RX stays 0.
	if result, downErr := exec.CommandContext(ctx, ipCommand, "link", "set", "dev", networkInterface, "down").CombinedOutput(); downErr != nil {
		return fmt.Errorf("set %s down to enable qmi raw_ip: %w: %s", networkInterface, downErr, strings.TrimSpace(string(result)))
	}
	if !enabled {
		if err := os.WriteFile(path, []byte("Y\n"), 0o644); err != nil {
			return fmt.Errorf("write %s qmi/raw_ip: %w%s", networkInterface, err, readOnlySysfsHint(err))
		}
	}
	_, enabled, err = readKernelRawIP(path)
	if err != nil || !enabled {
		value := ""
		if err != nil {
			value = err.Error()
		}
		return fmt.Errorf("failed to set %s qmi/raw_ip=Y (now %q); the modem will transmit but never receive", networkInterface, value)
	}
	prepareRawIPLink(ctx, ipCommand, networkInterface)
	return nil
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
	gateway := ""
	if lease.Gateway != nil {
		gateway = lease.Gateway.String()
	}
	_, err := configureExportProxyIPv4(ctx, ipCommand, networkInterface, lease.Address.String(), lease.prefixLen(), gateway, lease.DNS)
	return err
}

func qmiCallInUse(detail string) bool {
	lower := strings.ToLower(detail)
	return strings.Contains(lower, "interface-in-use") ||
		strings.Contains(lower, "interface in use") ||
		strings.Contains(lower, "callfailed") ||
		strings.Contains(lower, "out of call")
}
