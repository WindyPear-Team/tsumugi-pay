package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestFetchWechatPlatformCertificate(t *testing.T) {
	merchantKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate merchant key: %v", err)
	}
	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate platform key: %v", err)
	}
	now := time.Now().UTC()
	certificateDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(42),
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
	}, &x509.Certificate{SerialNumber: big.NewInt(42)}, &platformKey.PublicKey, platformKey)
	if err != nil {
		t.Fatalf("create platform certificate: %v", err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	apiKey := "12345678901234567890123456789012"
	associatedData := "certificate"
	nonce := []byte("123456789012")
	block, err := aes.NewCipher([]byte(apiKey))
	if err != nil {
		t.Fatalf("create cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("create GCM: %v", err)
	}
	ciphertext := gcm.Seal(nil, nonce, certificatePEM, []byte(associatedData))
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.mch.weixin.qq.com" || request.URL.Path != "/v3/certificates" || request.Method != http.MethodGet {
			t.Fatalf("unexpected platform certificate request: %s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") == "" {
			t.Fatal("platform certificate request must be signed")
		}
		body, _ := json.Marshal(map[string]any{"data": []map[string]any{{
			"serial_no":      "PLATFORM-SERIAL-42",
			"effective_time": now.Add(-time.Hour).Format(time.RFC3339),
			"expire_time":    now.Add(time.Hour).Format(time.RFC3339),
			"encrypt_certificate": map[string]string{
				"algorithm":       "AEAD_AES_256_GCM",
				"nonce":           string(nonce),
				"associated_data": associatedData,
				"ciphertext":      base64.StdEncoding.EncodeToString(ciphertext),
			},
		}}})
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	service := &Service{httpClient: client}
	merchantPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(merchantKey)})
	config := &wechatConfig{MchID: "1900000001", MerchantSerialNo: "MERCHANT-SERIAL", MerchantPrivateKeyPEM: string(merchantPEM), APIv3Key: apiKey}
	if err := service.fetchWechatPlatformCertificate(context.Background(), config); err != nil {
		t.Fatalf("fetch platform certificate: %v", err)
	}
	if config.PlatformSerialNo != "PLATFORM-SERIAL-42" {
		t.Fatalf("platform serial = %q, want PLATFORM-SERIAL-42", config.PlatformSerialNo)
	}
	publicKey, err := parsePublicKey(config.PlatformPublicKeyPEM)
	if err != nil {
		t.Fatalf("parse fetched public key: %v", err)
	}
	if publicKey.N.Cmp(platformKey.PublicKey.N) != 0 || publicKey.E != platformKey.PublicKey.E {
		t.Fatal("fetched public key does not match platform certificate")
	}
}

func TestPublicChannelConfigRedactsCredentials(t *testing.T) {
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("create cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("create GCM: %v", err)
	}
	service := &Service{cipher: gcm}
	config := providerConfig{Wechat: &wechatConfig{
		MchID: "1900000001", AppID: "wx123", MerchantSerialNo: "merchant-serial",
		MerchantPrivateKeyPEM: "merchant-private-secret", APIv3Key: "api-v3-secret", PlatformPublicKeyPEM: "platform-public-key",
	}}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("encode config: %v", err)
	}
	ciphertext, err := service.encrypt(string(encoded))
	if err != nil {
		t.Fatalf("encrypt config: %v", err)
	}
	view, err := json.Marshal(service.publicChannelConfig(ciphertext))
	if err != nil {
		t.Fatalf("encode public config: %v", err)
	}
	text := string(view)
	for _, secret := range []string{"merchant-private-secret", "api-v3-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("public config leaked credential %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "1900000001") || !strings.Contains(text, "platform-public-key") {
		t.Fatalf("public config did not return editable values: %s", text)
	}
}

func TestMergeProviderConfigPreservesUnchangedCredentials(t *testing.T) {
	stored := providerConfig{Wechat: &wechatConfig{
		MchID: "1900000001", AppID: "wx-old", MerchantPrivateKeyPEM: "private-old", APIv3Key: "api-key-old",
	}}
	merged := mergeProviderConfig("wechat", stored, providerConfig{Wechat: &wechatConfig{AppID: "wx-new"}})
	if merged.Wechat.AppID != "wx-new" || merged.Wechat.MerchantPrivateKeyPEM != "private-old" || merged.Wechat.APIv3Key != "api-key-old" {
		t.Fatalf("partial update overwrote stored credentials: %+v", merged.Wechat)
	}
}

func TestVerifyOPSSignatureSupportsEasyPayAndModernFields(t *testing.T) {
	secret := "merchant-key"
	legacy := paymentInput{MerchantID: "1000", PaymentMethod: "alipay", MerchantOrderNo: "LEGACY-001", Subject: "Legacy order", Amount: "9.90", NotifyURL: "https://merchant.example.test/notify"}
	legacyCanonical := canonicalValues(url.Values{"pid": {legacy.MerchantID}, "type": {legacy.PaymentMethod}, "out_trade_no": {legacy.MerchantOrderNo}, "name": {legacy.Subject}, "money": {legacy.Amount}, "notify_url": {legacy.NotifyURL}})
	legacy.Sign, _ = signOPS(legacyCanonical, secret, "MD5")
	if err := verifyOPSSignature(legacy, secret); err != nil {
		t.Fatalf("verify EasyPay MD5 signature without sign_type: %v", err)
	}
	legacyWithExtras := url.Values{"pid": {"1000"}, "type": {"alipay"}, "out_trade_no": {"LEGACY-EXTRA-001"}, "name": {"Legacy order"}, "money": {"9.90"}, "notify_url": {"https://merchant.example.test/notify"}, "sitename": {"Example Store"}, "clientip": {"127.0.0.1"}}
	extraSign, _ := signOPS(canonicalSignatureValues(legacyWithExtras), secret, "MD5")
	legacyWithExtras.Set("sign", extraSign)
	if err := verifyOPSSignature(inputFromValues(legacyWithExtras), secret); err != nil {
		t.Fatalf("verify EasyPay MD5 signature with extra fields: %v", err)
	}

	modern := paymentInput{MerchantID: "1000", PaymentMethod: "wechat", MerchantOrderNo: "MODERN-001", Subject: "Modern order", Description: "test", Amount: "12.34", NotifyURL: "https://merchant.example.test/notify", Metadata: "customer=42", Scene: "native", SignType: "HMAC-SHA256"}
	modernCanonical := canonicalValues(url.Values{"merchant_id": {modern.MerchantID}, "payment_method": {modern.PaymentMethod}, "merchant_order_no": {modern.MerchantOrderNo}, "subject": {modern.Subject}, "description": {modern.Description}, "amount": {modern.Amount}, "notify_url": {modern.NotifyURL}, "metadata": {modern.Metadata}, "scene": {modern.Scene}})
	modern.Sign, _ = signOPS(modernCanonical, secret, modern.SignType)
	if err := verifyOPSSignature(modern, secret); err != nil {
		t.Fatalf("verify modern HMAC signature: %v", err)
	}
}

func TestBillPollingIsThrottled(t *testing.T) {
	service := &Service{}
	billID := uuid.New()
	if !service.shouldPollBill(billID) {
		t.Fatal("first pending-order poll should run")
	}
	if service.shouldPollBill(billID) {
		t.Fatal("second immediate pending-order poll should be throttled")
	}
}
