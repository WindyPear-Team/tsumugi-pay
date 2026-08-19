package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type callbackSSRFConfig struct {
	Enabled      bool     `json:"enabled"`
	BlockedCIDRs []string `json:"blocked_cidrs"`
}

type callbackSSRFPolicy struct {
	blocked []netip.Prefix
}

func defaultCallbackSSRFConfig() callbackSSRFConfig {
	return callbackSSRFConfig{Enabled: true, BlockedCIDRs: []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"::/128", "100::/64", "2001:db8::/32",
	}}
}

func normalizeCallbackSSRFConfig(config callbackSSRFConfig) (callbackSSRFConfig, error) {
	result := callbackSSRFConfig{Enabled: config.Enabled, BlockedCIDRs: make([]string, 0, len(config.BlockedCIDRs))}
	seen := map[string]struct{}{}
	for _, raw := range config.BlockedCIDRs {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return callbackSSRFConfig{}, fmt.Errorf("blocked CIDR %q is invalid", value)
		}
		canonical := prefix.Masked().String()
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		result.BlockedCIDRs = append(result.BlockedCIDRs, canonical)
	}
	return result, nil
}

func newCallbackSSRFPolicy(config callbackSSRFConfig) (callbackSSRFPolicy, error) {
	policy := callbackSSRFPolicy{}
	for _, raw := range config.BlockedCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return callbackSSRFPolicy{}, fmt.Errorf("blocked CIDR %q is invalid", raw)
		}
		policy.blocked = append(policy.blocked, prefix.Masked())
	}
	return policy, nil
}

func (p callbackSSRFPolicy) validateURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("merchant callback URL is invalid")
	}
	return p.validateParsedURL(ctx, parsed)
}

func (p callbackSSRFPolicy) validateParsedURL(ctx context.Context, parsed *url.URL) error {
	if parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return errors.New("merchant callback URL must be an HTTP or HTTPS URL without credentials")
	}
	_, err := p.resolveAllowedIPs(ctx, parsed.Hostname())
	return err
}

func (p callbackSSRFPolicy) resolveAllowedIPs(ctx context.Context, host string) ([]net.IP, error) {
	if host == "" {
		return nil, errors.New("merchant callback URL has no host")
	}
	if address := net.ParseIP(host); address != nil {
		if p.blockedIP(address) {
			return nil, errors.New("merchant callback address is blocked by SSRF policy")
		}
		return []net.IP{address}, nil
	}
	addresses, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("merchant callback host cannot be resolved")
	}
	for _, address := range addresses {
		if p.blockedIP(address) {
			return nil, errors.New("merchant callback host resolves to a blocked address")
		}
	}
	return addresses, nil
}

func (p callbackSSRFPolicy) blockedIP(address net.IP) bool {
	if address == nil || !address.IsGlobalUnicast() || address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return true
	}
	parsed, ok := netip.AddrFromSlice(address)
	if !ok {
		return true
	}
	for _, prefix := range p.blocked {
		if prefix.Contains(parsed.Unmap()) {
			return true
		}
	}
	return false
}

func (p callbackSSRFPolicy) client() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := p.resolveAllowedIPs(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, resolved := range addresses {
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			return p.validateParsedURL(request.Context(), request.URL)
		},
	}
}
