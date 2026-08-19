package app

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

func TestDefaultCallbackSSRFConfigIsEnabled(t *testing.T) {
	if !defaultCallbackSSRFConfig().Enabled {
		t.Fatal("callback SSRF protection must be enabled by default")
	}
}

func TestCallbackSSRFPolicyBlocksPrivateAndConfiguredRanges(t *testing.T) {
	config, err := normalizeCallbackSSRFConfig(callbackSSRFConfig{Enabled: true, BlockedCIDRs: []string{" 100.64.0.0/10 ", "100.64.0.0/10"}})
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if len(config.BlockedCIDRs) != 1 || config.BlockedCIDRs[0] != "100.64.0.0/10" {
		t.Fatalf("normalized CIDRs = %#v", config.BlockedCIDRs)
	}
	policy, err := newCallbackSSRFPolicy(config)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	for _, raw := range []string{
		"http://127.0.0.1/callback",
		"http://10.0.0.1/callback",
		"http://169.254.169.254/latest/meta-data",
		"https://100.64.0.1/callback",
	} {
		if err := policy.validateURL(context.Background(), raw); err == nil {
			t.Fatalf("policy accepted blocked callback URL %q", raw)
		}
	}
	if err := policy.validateURL(context.Background(), "https://8.8.8.8/callback"); err != nil {
		t.Fatalf("policy rejected public callback URL: %v", err)
	}
}

func TestCallbackSSRFPolicyChecksRedirectTargets(t *testing.T) {
	policy, err := newCallbackSSRFPolicy(defaultCallbackSSRFConfig())
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	client := policy.client()
	request := &http.Request{URL: &url.URL{Scheme: "http", Host: "127.0.0.1"}}
	if err := client.CheckRedirect(request, nil); err == nil {
		t.Fatal("redirect to loopback address must be blocked")
	}
}

func TestNormalizeCallbackSSRFConfigRejectsInvalidCIDR(t *testing.T) {
	if _, err := normalizeCallbackSSRFConfig(callbackSSRFConfig{BlockedCIDRs: []string{"not-a-cidr"}}); err == nil {
		t.Fatal("invalid CIDR must be rejected")
	}
}

func TestSaveSiteConfigPersistsCallbackSSRFSettings(t *testing.T) {
	database, err := OpenDatabase("sqlite", "file:tsumugi_callback_ssrf_settings_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	store := NewStore(database)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	service, err := New(Config{Database: store, JWTSecret: "test-secret", EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	config := defaultSiteConfig()
	config.CallbackSSRF = callbackSSRFConfig{Enabled: true, BlockedCIDRs: []string{"198.18.0.0/15"}}
	if err := service.saveSiteConfig(context.Background(), config); err != nil {
		t.Fatalf("save site config: %v", err)
	}
	loaded, err := service.loadSiteConfig(context.Background())
	if err != nil {
		t.Fatalf("load site config: %v", err)
	}
	if !loaded.CallbackSSRF.Enabled || len(loaded.CallbackSSRF.BlockedCIDRs) != 1 || loaded.CallbackSSRF.BlockedCIDRs[0] != "198.18.0.0/15" {
		t.Fatalf("loaded callback SSRF config = %#v", loaded.CallbackSSRF)
	}
}
