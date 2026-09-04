package ims

import (
	"net"
	"testing"
)

func TestNormalizeDTMFDigits(t *testing.T) {
	got, err := normalizeDTMFDigits(" 1*#A ")
	if err != nil || got != "1*#A" {
		t.Fatalf("normalizeDTMFDigits = %q, %v", got, err)
	}
	if _, err := normalizeDTMFDigits("9;"); err == nil {
		t.Fatal("expected invalid DTMF")
	}
	if _, err := normalizeDTMFDigits(""); err == nil {
		t.Fatal("expected empty DTMF error")
	}
}

func TestDTMFEventCode(t *testing.T) {
	cases := map[rune]byte{'0': 0, '5': 5, '*': 10, '#': 11, 'A': 12, 'd': 15}
	for digit, want := range cases {
		got, ok := dtmfEventCode(digit)
		if !ok || got != want {
			t.Fatalf("dtmfEventCode(%q) = %d, %v", digit, got, ok)
		}
	}
}

func TestSendDTMFWritesRFC4733(t *testing.T) {
	left, err := newRTPMedia(net.IPv4(127, 0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := newRTPMedia(net.IPv4(127, 0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()
	if err := left.configureRemote(right.offerSDP(net.IPv4(127, 0, 0, 1))); err != nil {
		t.Fatal(err)
	}
	if err := left.SendDTMF("5"); err != nil {
		t.Fatal(err)
	}
}

func TestParseAudioSDPCapturesTelephoneEvent(t *testing.T) {
	left, err := newRTPMedia(net.IPv4(127, 0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	sdp := []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=t\r\nc=IN IP4 127.0.0.1\r\nm=audio 4000 RTP/AVP 8 101\r\na=rtpmap:8 PCMA/8000\r\na=rtpmap:101 telephone-event/8000\r\n")
	if err := left.configureRemote(sdp); err != nil {
		t.Fatal(err)
	}
	if left.dtmfPT != 101 {
		t.Fatalf("dtmfPT = %d, want 101", left.dtmfPT)
	}
	if left.codec != "PCMA" {
		t.Fatalf("codec = %q, want PCMA", left.codec)
	}
}
