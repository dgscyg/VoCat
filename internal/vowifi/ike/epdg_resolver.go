package ike

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"vocat/internal/vowifi"
)

const googleDNSOverHTTPS = "https://dns.google/resolve"

type dnsOverHTTPSResponse struct {
	Status int `json:"Status"`
	Answer []struct {
		Type int    `json:"type"`
		Data string `json:"data"`
	} `json:"Answer"`
}

func resolveEPDG(ctx context.Context, resolver *net.Resolver, host string) ([]net.IPAddr, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, systemErr := resolver.LookupIPAddr(ctx, host)
	if systemErr == nil && len(addresses) > 0 {
		return addresses, nil
	}

	normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	subnet := vowifi.EPDGDNSClientSubnet(normalized)
	if subnet == "" {
		if systemErr != nil {
			return nil, systemErr
		}
		return nil, errors.New("ePDG did not resolve to an IP address")
	}

	client := &http.Client{Timeout: 8 * time.Second}
	var fallbackErr error
	// Vodafone's authoritative response has a 60-second TTL and recursive
	// resolvers can briefly cache the global CNAME without its geo-restricted
	// address records. Stay inside the runtime's two-minute setup window and
	// wait through one complete negative-cache TTL so a single reconnect is
	// sufficient; users should not have to click Reconnect repeatedly.
	const fallbackAttempts = 13
	for attempt := 0; attempt < fallbackAttempts; attempt++ {
		var fallback []net.IPAddr
		fallback, fallbackErr = resolveEPDGWithECS(ctx, client, googleDNSOverHTTPS, normalized, subnet)
		if fallbackErr == nil && len(fallback) > 0 {
			return fallback, nil
		}
		if attempt+1 < fallbackAttempts {
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	if systemErr == nil {
		systemErr = errors.New("system DNS returned no IP addresses")
	}
	return nil, fmt.Errorf("system DNS failed (%v); geographic DNS fallback failed: %w", systemErr, fallbackErr)
}

func resolveEPDGWithECS(
	ctx context.Context,
	client *http.Client,
	endpoint, host, subnet string,
) ([]net.IPAddr, error) {
	if client == nil {
		return nil, errors.New("nil DNS-over-HTTPS client")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse DNS-over-HTTPS endpoint: %w", err)
	}
	query := parsed.Query()
	query.Set("name", strings.TrimSpace(host))
	query.Set("type", "A")
	query.Set("edns_client_subnet", strings.TrimSpace(subnet))
	parsed.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build DNS-over-HTTPS request: %w", err)
	}
	request.Header.Set("Accept", "application/dns-json")
	request.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query DNS-over-HTTPS: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DNS-over-HTTPS returned HTTP %d", response.StatusCode)
	}
	var payload dnsOverHTTPSResponse
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode DNS-over-HTTPS response: %w", err)
	}
	if payload.Status != 0 {
		return nil, fmt.Errorf("DNS-over-HTTPS returned DNS status %d", payload.Status)
	}
	result := make([]net.IPAddr, 0, len(payload.Answer))
	for _, answer := range payload.Answer {
		if answer.Type != 1 && answer.Type != 28 {
			continue
		}
		ip := net.ParseIP(strings.TrimSuffix(strings.TrimSpace(answer.Data), "."))
		if ip == nil {
			continue
		}
		duplicate := false
		for _, existing := range result {
			if existing.IP.Equal(ip) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, net.IPAddr{IP: append(net.IP(nil), ip...)})
		}
	}
	if len(result) == 0 {
		return nil, errors.New("DNS-over-HTTPS response contained no ePDG IP addresses")
	}
	return result, nil
}
