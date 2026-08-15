//go:build !linux

package exportproxy

import (
	"context"
	"errors"
	"net"
)

func platformSupported() error      { return errors.New("built-in export proxy is only available on Linux") }
func boundDialer(string) net.Dialer { return net.Dialer{} }
func prepareInterfaceDNS(string)    {}

func interfaceDialReady(string) error { return nil }

func lookupBoundIPs(ctx context.Context, _, host string) ([]net.IPAddr, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IPAddr{{IP: ip}}, nil
	}
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}
