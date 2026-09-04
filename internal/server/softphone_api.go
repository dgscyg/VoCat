package server

import (
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"vocat/internal/store"
)

func (s *Server) handleSoftphoneInfo(w http.ResponseWriter, r *http.Request, config store.Device) bool {
	if !requireMethod(w, r, http.MethodGet) {
		return true
	}
	if s.sipgw == nil || s.callTransport(config.ID) != "vowifi" {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"enabled": false}})
		return true
	}
	host := r.Host
	creds := s.sipgw.Credentials(config.ID, host)
	scheme := "wss"
	if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "ws"
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"enabled":  true,
		"username": creds.Username,
		"password": creds.Password,
		"realm":    creds.Realm,
		"ws_url":   scheme + "://" + host + creds.WSSPath,
		"udp_port": creds.UDPort,
		"host":     strings.TrimSuffix(strings.Split(host, ":")[0], "]"),
	}})
	return true
}

func (s *Server) handleSIPWebSocket(w http.ResponseWriter, r *http.Request, config store.Device) bool {
	if s.sipgw == nil {
		writeError(w, http.StatusServiceUnavailable, "sip_gateway_unavailable", "the SIP gateway is unavailable")
		return true
	}
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return true
	}
	defer connection.Close(websocket.StatusNormalClosure, "sip closed")
	s.sipgw.AcceptWebSocket(r.Context(), connection, config.ID, r.Host)
	return true
}
