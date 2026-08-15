package exportproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const proxyTimeout = 30 * time.Second

func ensureHostPort(address, defaultPort string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return address
	}
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	host := address
	if ip := net.ParseIP(address); ip != nil {
		host = ip.String()
	}
	return net.JoinHostPort(host, defaultPort)
}

func dialTarget(ctx context.Context, address string, dialer *net.Dialer, networkInterface string) (net.Conn, error) {
	if err := interfaceDialReady(networkInterface); err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		network := "tcp"
		if ip.To4() != nil {
			network = "tcp4"
		} else {
			network = "tcp6"
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	ips, err := lookupBoundIPs(ctx, networkInterface, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		network := "tcp"
		if ip.IP.To4() != nil {
			network = "tcp4"
		}
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: no addresses for %s", errors.ErrUnsupported, host)
	}
	return nil, lastErr
}

func pipe(left, right net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = copyConnection(right, left); done <- struct{}{} }()
	go func() { _, _ = copyConnection(left, right); done <- struct{}{} }()
	<-done
}

func copyConnection(destination net.Conn, source net.Conn) (int64, error) {
	written, err := io.CopyBuffer(destination, source, make([]byte, 32*1024))
	if err == nil && written > 0 {
		if connection, ok := destination.(interface{ CloseWrite() error }); ok {
			_ = connection.CloseWrite()
		}
	}
	return written, err
}
