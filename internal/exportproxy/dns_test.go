package exportproxy

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

func TestEncodeAndParseDNSQueryA(t *testing.T) {
	query, id, err := encodeDNSQueryA("ipinfo.io")
	if err != nil {
		t.Fatal(err)
	}
	if len(query) < 12 || binary.BigEndian.Uint16(query[0:2]) != id {
		t.Fatalf("query = %x id=%d", query, id)
	}

	response := append([]byte{}, query...)
	response[2] = 0x81
	response[3] = 0x80
	binary.BigEndian.PutUint16(response[6:8], 1)
	response = append(response, 0xc0, 0x0c)
	response = append(response, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x3c, 0x00, 0x04)
	response = append(response, 203, 0, 113, 8)

	ips, err := parseDNSResponseA(response, id, "ipinfo.io")
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 || !ips[0].Equal(net.IPv4(203, 0, 113, 8)) {
		t.Fatalf("ips = %v", ips)
	}
}

func TestParseDNSResponseAAcceptsCNAMEThenA(t *testing.T) {
	query, id, err := encodeDNSQueryA("ipinfo.io")
	if err != nil {
		t.Fatal(err)
	}
	response := append([]byte{}, query...)
	response[2] = 0x81
	response[3] = 0x80
	binary.BigEndian.PutUint16(response[6:8], 2)

	// CNAME ipinfo.io -> cdn.test
	response = append(response, 0xc0, 0x0c, 0x00, 0x05, 0x00, 0x01, 0x00, 0x00, 0x00, 0x3c, 0x00, 0x0a)
	response = append(response, 3)
	response = append(response, "cdn"...)
	response = append(response, 4)
	response = append(response, "test"...)
	response = append(response, 0)

	// A cdn.test -> 203.0.113.8, owner name does not match the original QNAME.
	response = append(response, 3)
	response = append(response, "cdn"...)
	response = append(response, 4)
	response = append(response, "test"...)
	response = append(response, 0)
	response = append(response, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x3c, 0x00, 0x04)
	response = append(response, 203, 0, 113, 8)

	ips, err := parseDNSResponseA(response, id, "ipinfo.io")
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 || !ips[0].Equal(net.IPv4(203, 0, 113, 8)) {
		t.Fatalf("ips = %v", ips)
	}
}

func TestDecodeDNSJSON(t *testing.T) {
	ips, err := decodeDNSJSON(strings.NewReader(`{"Status":0,"Answer":[{"name":"ipv4.ip.sb","type":1,"TTL":168,"data":"104.26.13.31"},{"name":"ipv4.ip.sb","type":5,"data":"ignored.example"},{"name":"ipv4.ip.sb","type":1,"TTL":168,"data":"172.67.75.172"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 2 || ips[0].String() != "104.26.13.31" || ips[1].String() != "172.67.75.172" {
		t.Fatalf("ips = %v", ips)
	}
}

func TestParseDNSResponseARejectsRcode(t *testing.T) {
	query, id, err := encodeDNSQueryA("missing.example")
	if err != nil {
		t.Fatal(err)
	}
	query[2] = 0x81
	query[3] = 0x83
	if _, err := parseDNSResponseA(query, id, "missing.example"); err == nil {
		t.Fatal("NXDOMAIN was accepted")
	}
}
