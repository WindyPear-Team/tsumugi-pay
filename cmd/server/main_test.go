package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadConfigDisablesDemoBootstrapByDefault(t *testing.T) {
	t.Setenv("BOOTSTRAP_DEMO", "")
	t.Setenv("APP_ENV", "development")
	t.Setenv("APP_ENCRYPTION_KEY", "")
	t.Setenv("JWT_SECRET", "")

	config, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if config.BootstrapDemo {
		t.Fatal("BOOTSTRAP_DEMO must default to false")
	}
}

func TestLoadConfigRejectsDemoBootstrapInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("BOOTSTRAP_DEMO", "true")
	t.Setenv("JWT_SECRET", "production-test-jwt-secret")
	t.Setenv("APP_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))

	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), "BOOTSTRAP_DEMO") {
		t.Fatalf("loadConfig() error = %v, want production demo bootstrap rejection", err)
	}
}
