package exportproxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

func serveHTTP(client net.Conn, config Config, dialer *net.Dialer) error {
	reader := bufio.NewReader(client)
	request, err := http.ReadRequest(reader)
	if err != nil {
		return err
	}
	if config.AuthEnabled && !httpAuthorized(request, config) {
		_, _ = client.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"vocat-export-proxy\"\r\n\r\n"))
		return errors.New("HTTP proxy authentication required")
	}
	if request.Method == http.MethodConnect {
		ctx, cancel := context.WithTimeout(context.Background(), proxyTimeout)
		target, err := dialTarget(ctx, ensureHostPort(request.URL.Host, "443"), dialer, config.Interface)
		cancel()
		if err != nil {
			_, _ = fmt.Fprint(client, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
			return err
		}
		defer target.Close()
		if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			return err
		}
		if buffered := reader.Buffered(); buffered > 0 {
			if value, err := reader.Peek(buffered); err == nil {
				_, _ = target.Write(value)
				_, _ = reader.Discard(buffered)
			}
		}
		pipe(client, target)
		return nil
	}

	request.Header.Del("Proxy-Authorization")
	request.Header.Del("Proxy-Connection")
	request.RequestURI = ""
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialTarget(ctx, address, dialer, config.Interface)
		},
		DisableKeepAlives: true,
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		_, _ = fmt.Fprint(client, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return err
	}
	defer response.Body.Close()
	return response.Write(client)
}

func httpAuthorized(request *http.Request, config Config) bool {
	header := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Proxy-Authorization"), "Basic "))
	decoded, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	return len(parts) == 2 && parts[0] == config.Username && parts[1] == config.Password
}
