package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vocat/internal/device"
	"vocat/internal/modem"
	"vocat/internal/store"
)

type discoverySnapshotController struct {
	fakeDeviceController
	entries       []device.Device
	discoverCalls int
}

func (controller *discoverySnapshotController) Discover(context.Context) ([]device.Device, error) {
	controller.discoverCalls++
	return append([]device.Device(nil), controller.entries...), nil
}

func TestDiscoveredDevicesPerformsFreshScanAndOmitsAbsentEntries(t *testing.T) {
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	controller := &discoverySnapshotController{
		fakeDeviceController: fakeDeviceController{entry: device.Device{
			ID: "stale-device", Discovered: false,
			Candidate: modem.Candidate{ID: "stale-device", USBPath: "1-1"},
		}},
		entries: []device.Device{
			{
				ID: "current-device", Discovered: true,
				Candidate: modem.Candidate{ID: "current-device", USBPath: "2-1"},
			},
			{
				ID: "absent-device", Discovered: false,
				Candidate: modem.Candidate{ID: "absent-device", USBPath: "3-1"},
			},
		},
	}
	server := &Server{
		store: database, logger: regionTestLogger(),
		maxRequestBodyBytes: 4096, devices: controller,
	}
	request := httptest.NewRequest(http.MethodGet, "/api/devices/discovered", nil)
	recorder := httptest.NewRecorder()

	if !server.handleDiscoveredDevices(recorder, request) {
		t.Fatal("handleDiscoveredDevices returned false")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if controller.discoverCalls != 1 {
		t.Fatalf("Discover calls = %d, want 1", controller.discoverCalls)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "current-device") {
		t.Fatalf("response omits current device: %s", body)
	}
	if strings.Contains(body, "stale-device") || strings.Contains(body, "absent-device") {
		t.Fatalf("response contains an absent device: %s", body)
	}
}
