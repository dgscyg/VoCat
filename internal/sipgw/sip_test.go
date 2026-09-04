package sipgw

import "testing"

func TestParseSIPInvite(t *testing.T) {
	raw := "INVITE sip:15551212@example.com SIP/2.0\r\nVia: SIP/2.0/WSS host\r\nFrom: <sip:webrtc@example.com>;tag=abc\r\nTo: <sip:15551212@example.com>\r\nCall-ID: 1\r\nCSeq: 2 INVITE\r\nContent-Length: 3\r\n\r\nv=0"
	msg, err := parseSIP(raw)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Method != "INVITE" || userFromURI(msg.URI) != "15551212" || string(msg.Body) != "v=0" {
		t.Fatalf("parsed = %+v body=%q", msg, msg.Body)
	}
	if msg.fromTag() != "abc" {
		t.Fatalf("from tag = %q", msg.fromTag())
	}
}

func TestDigestResponse(t *testing.T) {
	got := digestResponse("webrtc", "vocat", "secret", "n", "sip:vocat", "REGISTER", "", "", "")
	if got == "" || len(got) != 32 {
		t.Fatalf("digest = %q", got)
	}
}

func TestUDPPortStable(t *testing.T) {
	a := udpPortFor("ec20")
	b := udpPortFor("ec20")
	if a != b || a < 15060 || a >= 15960 {
		t.Fatalf("udp port = %d", a)
	}
}
