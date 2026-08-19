package device

import (
	"bytes"
	"testing"
)

func TestCATRefreshTerminalResponse(t *testing.T) {
	raw := []byte{
		0x44, 0x33, 0x22, 0x11, // reference
		0x0B, 0x00, // command length
		0xD0, 0x09,
		0x81, 0x03, 0x07, 0x01, 0x00,
		0x82, 0x02, 0x81, 0x82,
	}
	reference, response, ok := catRefreshTerminalResponse(raw)
	if !ok {
		t.Fatal("catRefreshTerminalResponse() did not recognize REFRESH")
	}
	if reference != 0x11223344 {
		t.Fatalf("reference = 0x%08X", reference)
	}
	want := []byte{
		0x81, 0x03, 0x07, 0x01, 0x00,
		0x82, 0x02, 0x82, 0x81,
		0x83, 0x01, 0x00,
	}
	if !bytes.Equal(response, want) {
		t.Fatalf("response = % X, want % X", response, want)
	}
}

func TestCATRefreshTerminalResponseRejectsOtherCommands(t *testing.T) {
	raw := []byte{
		0x01, 0x00, 0x00, 0x00,
		0x0B, 0x00,
		0xD0, 0x09,
		0x81, 0x03, 0x01, 0x21, 0x00, // DISPLAY TEXT
		0x82, 0x02, 0x81, 0x02,
	}
	if _, _, ok := catRefreshTerminalResponse(raw); ok {
		t.Fatal("catRefreshTerminalResponse() accepted a non-REFRESH command")
	}
}

func TestCATRefreshTerminalResponseSupportsLongBERLength(t *testing.T) {
	command := []byte{0xD0, 0x81, 0x09, 0x81, 0x03, 0x02, 0x01, 0x01, 0x82, 0x02, 0x81, 0x82}
	raw := append([]byte{0x02, 0x00, 0x00, 0x00, byte(len(command)), 0x00}, command...)
	if _, _, ok := catRefreshTerminalResponse(raw); !ok {
		t.Fatal("catRefreshTerminalResponse() rejected 0x81 BER length")
	}
}
