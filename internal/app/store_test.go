package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSQLiteSchemaAndBootstrap(t *testing.T) {
	database, err := OpenDatabase("sqlite", "file:tsumugi_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	store := NewStore(database)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	service, err := New(Config{
		Database: store, JWTSecret: "test-secret", EncryptionKey: make([]byte, 32),
		PublicBaseURL: "http://localhost:8080", Environment: "development", BootstrapDemo: true,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if err := service.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	var users, channels int
	if err := store.QueryRow(context.Background(), `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := store.QueryRow(context.Background(), `SELECT COUNT(*) FROM payment_channels`).Scan(&channels); err != nil {
		t.Fatalf("count channels: %v", err)
	}
	if users != 2 || channels != 0 {
		t.Fatalf("unexpected demo data: users=%d channels=%d", users, channels)
	}
}

func TestNormalizeSQL(t *testing.T) {
	query := normalizeSQL("mysql", `UPDATE bills SET updated_at=NOW() WHERE id=$1::uuid`)
	if query != "UPDATE bills SET updated_at=CURRENT_TIMESTAMP WHERE id=?" {
		t.Fatalf("unexpected normalized query: %s", query)
	}
}

func TestOOBECreatesInitialUserOnce(t *testing.T) {
	database, err := OpenDatabase("sqlite", "file:tsumugi_oobe_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	store := NewStore(database)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	service, err := New(Config{Database: store, JWTSecret: "test-secret", EncryptionKey: make([]byte, 32), PublicBaseURL: "http://localhost:8080", Environment: "development", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	handler := service.Routes()
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	if status.Code != http.StatusOK || !bytes.Contains(status.Body.Bytes(), []byte(`"required":true`)) {
		t.Fatalf("unexpected initial setup status: %d %s", status.Code, status.Body.String())
	}
	payload, _ := json.Marshal(map[string]string{"account_name": "First User", "display_name": "First User", "email": "first@example.test", "password": "A-strong-password"})
	setup := httptest.NewRecorder()
	handler.ServeHTTP(setup, httptest.NewRequest(http.MethodPost, "/api/v1/setup/initialize", bytes.NewReader(payload)))
	if setup.Code != http.StatusCreated {
		t.Fatalf("initialize setup: %d %s", setup.Code, setup.Body.String())
	}
	retry := httptest.NewRecorder()
	handler.ServeHTTP(retry, httptest.NewRequest(http.MethodPost, "/api/v1/setup/initialize", bytes.NewReader(payload)))
	if retry.Code != http.StatusConflict {
		t.Fatalf("setup endpoint should close after initialization: %d %s", retry.Code, retry.Body.String())
	}
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(payload)))
	if login.Code != http.StatusOK {
		t.Fatalf("login with OOBE administrator: %d %s", login.Code, login.Body.String())
	}
	var loginResponse struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &loginResponse); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	channelPayload := bytes.NewBufferString(`{"provider":"alipay","display_name":"支付宝"}`)
	channelRequest := httptest.NewRequest(http.MethodPost, "/api/v1/admin/channels", channelPayload)
	channelRequest.Header.Set("Authorization", "Bearer "+loginResponse.AccessToken)
	channelRequest.Header.Set("Content-Type", "application/json")
	channel := httptest.NewRecorder()
	handler.ServeHTTP(channel, channelRequest)
	if channel.Code != http.StatusCreated {
		t.Fatalf("create user payment channel: %d %s", channel.Code, channel.Body.String())
	}
	settingsPayload := bytes.NewBufferString(`{"email":{"host":"smtp.example.test","port":587,"username":"mailer","password":"smtp-password","from":"pay@example.test"},"hcaptcha":{"site_key":"site-key","secret_key":"captcha-secret"},"oidc":{"issuer_url":"https://id.example.test","client_id":"pay","client_secret":"oidc-secret","redirect_url":"https://pay.example.test/callback"}}`)
	settingsRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/site-settings", settingsPayload)
	settingsRequest.Header.Set("Authorization", "Bearer "+loginResponse.AccessToken)
	settingsRequest.Header.Set("Content-Type", "application/json")
	settingsResponse := httptest.NewRecorder()
	handler.ServeHTTP(settingsResponse, settingsRequest)
	if settingsResponse.Code != http.StatusOK || bytes.Contains(settingsResponse.Body.Bytes(), []byte("smtp-password")) || bytes.Contains(settingsResponse.Body.Bytes(), []byte("captcha-secret")) || bytes.Contains(settingsResponse.Body.Bytes(), []byte("oidc-secret")) {
		t.Fatalf("save settings should succeed without returning secrets: %d %s", settingsResponse.Code, settingsResponse.Body.String())
	}
}
