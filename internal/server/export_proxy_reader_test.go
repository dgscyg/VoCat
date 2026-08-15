package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vocat/internal/exportproxy"
	"vocat/internal/store"
)

func TestExportProxyRejectsUSBSIMReader(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(context.Background(), store.Device{
		ID: "reader-1", Name: "USB SIM Reader", DeviceType: store.DeviceTypeUSBSIMReader,
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: database}
	response := httptest.NewRecorder()
	if !server.rejectUnsupportedExportProxyDevice(response, context.Background(), "reader-1") {
		t.Fatal("reader was accepted as an export-proxy device")
	}
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestExportProxyAPIAvailableWithoutDeveloperMode(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertDevice(ctx, store.Device{
		ID: "modem-1", Name: "EC20", Interface: "wwan0", NetworkEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := exportproxy.New(ctx, database, slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	server := &Server{store: database, exportProxy: manager, maxRequestBodyBytes: 1 << 20}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/export-proxies", nil)
	if !server.routeExportProxyAPI(recorder, request, "export-proxies") {
		t.Fatal("export-proxies was not handled")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", recorder.Code, recorder.Body)
	}

	body, err := json.Marshal(exportproxy.Config{
		Name: "EC20 exit", DeviceID: "modem-1", Mode: "socks5",
		ListenHost: "127.0.0.1", ListenPort: 0, Enabled: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/export-proxies", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	if !server.routeExportProxyAPI(recorder, request, "export-proxies") {
		t.Fatal("export-proxies create was not handled")
	}
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body)
	}
}
