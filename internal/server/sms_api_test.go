package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vocat/internal/developer"
	"vocat/internal/device"
	"vocat/internal/store"
)

func TestSMSThreadAllDevicesUsesIMSIFilter(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for index, imsi := range []string{"imsi-a", "imsi-b"} {
		if _, err := database.SaveSMSMessage(ctx, store.SMSMessage{
			MessageID: "message-" + imsi,
			DeviceID:  "ec20",
			IMSI:      imsi,
			Peer:      "VOXI",
			Direction: "inbound",
			Body:      imsi,
			Timestamp: time.Unix(1_700_000_000+int64(index), 0),
		}); err != nil {
			t.Fatal(err)
		}
	}

	server := &Server{store: database}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/sms/thread?device_id=all&imsi=imsi-a&peer=VOXI",
		nil,
	)
	response := httptest.NewRecorder()
	server.handleSMSThread(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 1 || envelope.Data[0]["imsi"] != "imsi-a" {
		t.Fatalf("thread data = %#v", envelope.Data)
	}
}

func TestNative410DoesNotUseModemSMSStorage(t *testing.T) {
	if supportsModemSMSStorage(store.Device{DeviceType: store.DeviceTypeWiFi410}) {
		t.Fatal("native OpenStick 410 unexpectedly enabled modem SMS storage polling")
	}
	if !supportsModemSMSStorage(store.Device{DeviceType: store.DeviceTypePCIeEC20EC25}) {
		t.Fatal("EC20 modem SMS storage polling was disabled")
	}
}

func TestSMSThreadConfiguredDeviceUsesStableIMEI(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	const imei = "867394042309830"
	if err := database.UpsertDevice(ctx, store.Device{
		ID: "ec20_2", Name: "EC20 renamed", ModemIMEI: imei,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveSMSMessage(ctx, store.SMSMessage{
		MessageID: "before-rename", DeviceID: "ec20_1", ModemIMEI: imei,
		IMSI: "imsi-a", Peer: "VOXI", Direction: "inbound", Body: "history",
	}); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: database}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/sms/thread?device_id=ec20_2&imsi=imsi-a&peer=VOXI",
		nil,
	)
	response := httptest.NewRecorder()
	server.handleSMSThread(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 1 || envelope.Data[0]["modem_imei"] != imei {
		t.Fatalf("thread data = %#v", envelope.Data)
	}
}

func TestNormalizeSMSDeviceFilter(t *testing.T) {
	if got := normalizeSMSDeviceFilter(" ALL "); got != "" {
		t.Fatalf("all filter = %q", got)
	}
	if got := normalizeSMSDeviceFilter("EC20"); got != "EC20" {
		t.Fatalf("device filter = %q", got)
	}
}

func TestSupportsModemSMSStorageRejectsUSBReader(t *testing.T) {
	if supportsModemSMSStorage(store.Device{DeviceType: store.DeviceTypeUSBSIMReader}) {
		t.Fatal("USB SIM reader must not be polled with modem SMS AT commands")
	}
	if !supportsModemSMSStorage(store.Device{DeviceType: store.DeviceTypePCIeEC20EC25}) {
		t.Fatal("cellular modem should retain modem SMS storage synchronization")
	}
}

func TestSMSSendOutcome(t *testing.T) {
	tests := []struct {
		name      string
		all       bool
		accepted  int
		total     int
		delivered bool
		want      string
	}{
		{name: "delivered", all: true, accepted: 1, total: 1, delivered: true, want: "delivered"},
		{name: "accepted but unconfirmed", all: true, accepted: 2, total: 2, want: "accepted_unconfirmed"},
		{name: "partial", accepted: 1, total: 2, want: "partial"},
		{name: "failed", total: 1, want: "failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := smsSendOutcome(test.all, test.accepted, test.total, test.delivered); got != test.want {
				t.Fatalf("smsSendOutcome() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBlockedSMSDestination(t *testing.T) {
	tests := []struct {
		name  string
		phone string
		block bool
	}{
		{"e164 china", "+8613800138000", true},
		{"no plus china", "8613800138000", true},
		{"international prefix china", "008613800138000", true},
		{"spaced china", "+86 138 0013 8000", true},
		{"dashed china", "+86-138-0013-8000", true},
		{"us e164", "+12025550177", false},
		{"us no plus", "12025550177", false},
		{"uk e164", "+447700900123", false},
		{"italy", "+393331234567", false},
		{"russia", "+79161234567", false},
		{"japan", "+819012345678", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			blocked, _ := blockedSMSDestination(test.phone)
			if blocked != test.block {
				t.Fatalf("blockedSMSDestination(%q) blocked = %v, want %v", test.phone, blocked, test.block)
			}
		})
	}
}

func TestHandleSMSSendEnforcesGlobalHourlyLimit(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := developer.SetSMSHourlyLimit(ctx, database, 1); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertDevice(ctx, store.Device{ID: "ec20_1", Name: "EC20"}); err != nil {
		t.Fatal(err)
	}
	if reservation, err := database.ReserveSMSSend(ctx, "another-device", 1, time.Now().UTC()); err != nil || !reservation.Allowed {
		t.Fatalf("seed global SMS reservation = %+v, %v", reservation, err)
	}
	server := &Server{
		store:               database,
		logger:              regionTestLogger(),
		maxRequestBodyBytes: 4096,
		devices: fakeDeviceController{entry: device.Device{
			ID:         "ec20_1",
			Discovered: true,
			Snapshot:   &device.Snapshot{DeviceID: "ec20_1"},
		}},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/sms/send",
		strings.NewReader(`{"device_id":"ec20_1","phone":"+447700900123","message":"hello"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleSMSSend(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header is missing")
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "sms_rate_limited" {
		t.Fatalf("error code = %q, want sms_rate_limited", envelope.Error.Code)
	}
}
