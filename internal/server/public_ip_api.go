package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"vocat/internal/exportproxy"
	"vocat/internal/store"
)

type cachedPublicIP struct {
	ICCID string
	Info  exportproxy.PublicIPInfo
}

type publicIPResponse struct {
	Detected bool `json:"detected"`
	exportproxy.PublicIPInfo
}

func (s *Server) clearPublicIP(deviceID string) {
	s.publicIPMu.Lock()
	delete(s.publicIPs, strings.TrimSpace(deviceID))
	s.publicIPMu.Unlock()
}

func (s *Server) loadPublicIP(deviceID, iccid string) (exportproxy.PublicIPInfo, bool) {
	deviceID = strings.TrimSpace(deviceID)
	iccid = strings.TrimSpace(iccid)
	s.publicIPMu.RLock()
	entry, ok := s.publicIPs[deviceID]
	s.publicIPMu.RUnlock()
	if !ok {
		return exportproxy.PublicIPInfo{}, false
	}
	// A missing live ICCID means the modem is resetting or no card is present.
	// A different ICCID means the SIM/eSIM profile changed. Either transition
	// invalidates the old cellular exit immediately.
	if iccid == "" || !strings.EqualFold(strings.TrimSpace(entry.ICCID), iccid) {
		s.clearPublicIP(deviceID)
		return exportproxy.PublicIPInfo{}, false
	}
	return entry.Info, true
}

func (s *Server) savePublicIP(deviceID, iccid string, info exportproxy.PublicIPInfo) {
	s.publicIPMu.Lock()
	s.publicIPs[strings.TrimSpace(deviceID)] = cachedPublicIP{
		ICCID: strings.TrimSpace(iccid),
		Info:  info,
	}
	s.publicIPMu.Unlock()
}

func (s *Server) handleCellularPublicIP(w http.ResponseWriter, r *http.Request, config store.Device, iccid string) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		info, ok := s.loadPublicIP(config.ID, iccid)
		writeJSON(w, http.StatusOK, map[string]any{"data": publicIPResponse{Detected: ok, PublicIPInfo: info}})
		return true
	}
	if !config.NetworkEnabled {
		writeError(w, http.StatusConflict, "cellular_data_disabled", "enable roaming data before detecting its public IP")
		return true
	}
	if strings.TrimSpace(iccid) == "" {
		writeError(w, http.StatusConflict, "sim_identity_unavailable", "the modem has no current ICCID; refresh it before detecting the public IP")
		return true
	}
	if strings.TrimSpace(config.Interface) == "" {
		writeError(w, http.StatusConflict, "cellular_interface_missing", "the device has no cellular network interface")
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	info, err := exportproxy.LookupPublicIP(ctx, config.Interface)
	if err != nil {
		s.logger.Warn("detect roaming public IP failed", "device_id", config.ID, "interface", config.Interface, "error", err)
		writeError(w, http.StatusBadGateway, "public_ip_lookup_failed", err.Error())
		return true
	}
	s.savePublicIP(config.ID, iccid, info)
	writeJSON(w, http.StatusOK, map[string]any{"data": publicIPResponse{Detected: true, PublicIPInfo: info}})
	return true
}
