package exportproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const ipInfoURL = "https://ipinfo.io/json"

type PublicIPInfo struct {
	IP           string `json:"ip"`
	CountryCode  string `json:"country_code"`
	Region       string `json:"region"`
	City         string `json:"city"`
	Organization string `json:"organization,omitempty"`
}

// LookupPublicIP sends the lookup through the same marked, interface-bound
// dialer and isolated DNS resolver as Export Proxy. It therefore reports the
// modem's roaming exit rather than the host or browser's default connection.
func LookupPublicIP(ctx context.Context, networkInterface string) (PublicIPInfo, error) {
	networkInterface = strings.TrimSpace(networkInterface)
	if networkInterface == "" {
		return PublicIPInfo{}, errors.New("cellular network interface is required")
	}
	if err := platformSupported(); err != nil {
		return PublicIPInfo{}, err
	}
	if err := interfaceDialReady(networkInterface); err != nil {
		return PublicIPInfo{}, err
	}
	dialer := boundDialer(networkInterface)
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialTarget(ctx, address, &dialer, networkInterface)
		},
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 12 * time.Second,
	}
	defer transport.CloseIdleConnections()
	info, err := queryPublicIPEndpoint(ctx, transport, ipInfoURL, "application/json", decodePublicIPInfo)
	if err == nil {
		return info, nil
	}
	fallback, fallbackErr := queryPublicIPEndpoint(ctx, transport, cloudflareTraceURL, "text/plain", decodeCloudflareTrace)
	if fallbackErr == nil {
		return fallback, nil
	}
	return PublicIPInfo{}, fmt.Errorf("query public IP through %s: %w", networkInterface, errors.Join(err, fallbackErr))
}

const cloudflareTraceURL = "https://1.1.1.1/cdn-cgi/trace"

func queryPublicIPEndpoint(
	ctx context.Context,
	transport *http.Transport,
	rawURL, accept string,
	decode func(io.Reader) (PublicIPInfo, error),
) (PublicIPInfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return PublicIPInfo{}, err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("User-Agent", "VoCat/1.0")
	response, err := transport.RoundTrip(request)
	if err != nil {
		return PublicIPInfo{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return PublicIPInfo{}, fmt.Errorf("%s returned HTTP %d", request.URL.Host, response.StatusCode)
	}
	return decode(io.LimitReader(response.Body, 64<<10))
}

func decodePublicIPInfo(reader io.Reader) (PublicIPInfo, error) {
	var response struct {
		IP      string `json:"ip"`
		Country string `json:"country"`
		Region  string `json:"region"`
		City    string `json:"city"`
		Org     string `json:"org"`
	}
	if err := json.NewDecoder(reader).Decode(&response); err != nil {
		return PublicIPInfo{}, fmt.Errorf("decode ipinfo.io response: %w", err)
	}
	response.IP = strings.TrimSpace(response.IP)
	response.Country = strings.ToUpper(strings.TrimSpace(response.Country))
	if net.ParseIP(response.IP) == nil {
		return PublicIPInfo{}, errors.New("ipinfo.io response contained no valid IP address")
	}
	if len(response.Country) != 2 {
		return PublicIPInfo{}, errors.New("ipinfo.io response contained no valid country code")
	}
	return PublicIPInfo{
		IP: response.IP, CountryCode: response.Country,
		Region: strings.TrimSpace(response.Region), City: strings.TrimSpace(response.City),
		Organization: strings.TrimSpace(response.Org),
	}, nil
}

func decodeCloudflareTrace(reader io.Reader) (PublicIPInfo, error) {
	body, err := io.ReadAll(reader)
	if err != nil {
		return PublicIPInfo{}, err
	}
	info := PublicIPInfo{}
	for _, line := range strings.Split(string(body), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch key {
		case "ip":
			info.IP = strings.TrimSpace(value)
		case "loc":
			info.CountryCode = strings.ToUpper(strings.TrimSpace(value))
		}
	}
	if net.ParseIP(info.IP) == nil {
		return PublicIPInfo{}, errors.New("cloudflare trace contained no valid IP address")
	}
	if len(info.CountryCode) != 2 {
		info.CountryCode = ""
	}
	return info, nil
}
