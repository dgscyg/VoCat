package exportproxy

import (
	"strings"
	"testing"
)

func TestDecodePublicIPInfo(t *testing.T) {
	info, err := decodePublicIPInfo(strings.NewReader(`{"ip":"203.0.113.8","city":"London","region":"England","country":"gb","org":"AS64500 Test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.IP != "203.0.113.8" || info.CountryCode != "GB" || info.Region != "England" || info.City != "London" {
		t.Fatalf("info = %+v", info)
	}
}

func TestDecodePublicIPInfoRejectsInvalidResponse(t *testing.T) {
	if _, err := decodePublicIPInfo(strings.NewReader(`{"ip":"not-an-ip","country":"GB"}`)); err == nil {
		t.Fatal("invalid IP was accepted")
	}
}

func TestDecodeCloudflareTrace(t *testing.T) {
	info, err := decodeCloudflareTrace(strings.NewReader("fl=123\nh=1.1.1.1\nip=198.51.100.9\nloc=GB\n"))
	if err != nil {
		t.Fatal(err)
	}
	if info.IP != "198.51.100.9" || info.CountryCode != "GB" {
		t.Fatalf("info = %+v", info)
	}
}
