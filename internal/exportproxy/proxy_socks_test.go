package exportproxy

import (
	"errors"
	"testing"
)

func TestSocksReplyForDialError(t *testing.T) {
	cases := map[byte]error{
		3: errors.New("lookup ipv4.ip.sb through wwan0 via 8.8.8.8: dial udp4 8.8.8.8:53: network is unreachable"),
		4: errors.New("lookup ipv4.ip.sb through wwan0 via 8.8.8.8: no A records"),
		5: errors.New("connect: connection refused"),
		6: errors.New("i/o timeout"),
	}
	for want, err := range cases {
		if got := socksReplyForDialError(err); got != want {
			t.Errorf("socksReplyForDialError(%v) = %d, want %d", err, got, want)
		}
	}
}
