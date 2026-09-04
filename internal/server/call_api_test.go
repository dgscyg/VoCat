package server

import (
	"context"
	"testing"

	"vocat/internal/modem"
	"vocat/internal/vowifi"
)

func TestParseCLCC(t *testing.T) {
	calls := parseCLCC(modem.Response{Lines: []string{
		`+CLCC: 1,1,4,0,0,"+447700900000",145`,
		`+CLCC: 2,0,0,0,0,"12345",129`,
	}})
	if len(calls) != 2 || calls[0]["number"] != "+447700900000" || calls[1]["state"] != 0 {
		t.Fatalf("parseCLCC = %#v", calls)
	}
}

func TestValidDTMFDigits(t *testing.T) {
	if !validDTMFDigits("1*#A") || validDTMFDigits("") || validDTMFDigits("9;") {
		t.Fatal("validDTMFDigits mismatch")
	}
	if dtmfATArgument("5") != "5" || dtmfATArgument("12") != `"12"` {
		t.Fatalf("dtmfATArgument unexpected")
	}
}

func TestValidDialNumber(t *testing.T) {
	for _, value := range []string{"+447700900000", "12345", "*100#"} {
		if !validDialNumber(value) {
			t.Errorf("validDialNumber(%q) = false", value)
		}
	}
	for _, value := range []string{"", "+", "12;ATH", "12 34", "abc"} {
		if validDialNumber(value) {
			t.Errorf("validDialNumber(%q) = true", value)
		}
	}
}

func TestCallListPayloadMarksVoWiFiAudio(t *testing.T) {
	vowifi := callListPayload("ec20", "vowifi", []any{}, nil)
	if vowifi["audio_available"] != true {
		t.Fatalf("VoWiFi audio_available = %#v, want true", vowifi["audio_available"])
	}
	cellular := callListPayload("ec20", "cellular", []any{}, map[string]any{"raw": "OK"})
	if cellular["audio_available"] != false {
		t.Fatalf("cellular audio_available = %#v, want false", cellular["audio_available"])
	}
	if cellular["raw"] != "OK" {
		t.Fatalf("extra fields were dropped: %#v", cellular)
	}
}

func TestCallListPayloadCellularUSBAudioOverride(t *testing.T) {
	payload := callListPayload("ec20", "cellular", []any{}, map[string]any{"audio_available": true})
	if payload["audio_available"] != true {
		t.Fatalf("USB PCM audio_available = %#v, want true", payload["audio_available"])
	}
	if payload["transport"] != "cellular" {
		t.Fatalf("transport = %#v", payload["transport"])
	}
}

func TestCallTransportRequiresIMSReady(t *testing.T) {
	controller := &fakeVoWiFiController{state: vowifi.State{Enabled: true}}
	server := &Server{vowifi: controller}
	if got := server.callTransport("ec20"); got != "cellular" {
		t.Fatalf("callTransport before IMS registration = %q, want cellular", got)
	}
	controller.state.IMSReady = true
	if got := server.callTransport("ec20"); got != "vowifi" {
		t.Fatalf("callTransport with IMS ready = %q, want vowifi", got)
	}
}

func TestResolveVoWiFiCallIDIgnoresTerminalCalls(t *testing.T) {
	controller := &fakeCallController{calls: []vowifi.Call{
		{ID: "failed", State: "failed"},
		{ID: "active", State: "active"},
	}}
	got, err := resolveVoWiFiCallID(controller, "ec20", "", "")
	if err != nil || got != "active" {
		t.Fatalf("resolveVoWiFiCallID() = %q, %v; want active", got, err)
	}
}

type fakeCallController struct {
	calls []vowifi.Call
}

func (controller *fakeCallController) Calls(string) ([]vowifi.Call, error) {
	return controller.calls, nil
}

func (*fakeCallController) DialCall(context.Context, string, string) (vowifi.Call, error) {
	return vowifi.Call{}, nil
}

func (*fakeCallController) AnswerCall(context.Context, string, string) (vowifi.Call, error) {
	return vowifi.Call{}, nil
}

func (*fakeCallController) HangupCall(context.Context, string, string) error { return nil }

func (*fakeCallController) SendDTMF(context.Context, string, string, string) error { return nil }
