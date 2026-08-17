package app

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type channelRecord struct {
	ID           uuid.UUID
	AccountID    uuid.UUID
	Provider     string
	DisplayName  string
	Enabled      bool
	Config       providerConfig
	WebhookToken string
}

type providerConfig struct {
	Alipay *alipayConfig `json:"alipay,omitempty"`
	Wechat *wechatConfig `json:"wechat,omitempty"`
}

type alipayConfig struct {
	AppID              string `json:"app_id"`
	AppPrivateKeyPEM   string `json:"app_private_key_pem"`
	AlipayPublicKeyPEM string `json:"alipay_public_key_pem"`
	GatewayURL         string `json:"gateway_url"`
	ReturnURL          string `json:"return_url"`
}

type wechatConfig struct {
	MchID                 string `json:"mch_id"`
	AppID                 string `json:"app_id"`
	MerchantSerialNo      string `json:"merchant_serial_no"`
	MerchantPrivateKeyPEM string `json:"merchant_private_key_pem"`
	APIv3Key              string `json:"api_v3_key"`
	PlatformPublicKeyPEM  string `json:"platform_public_key_pem"`
	PlatformSerialNo      string `json:"platform_serial_no"`
}

type providerResult struct {
	ProviderTransactionID string
	PayURL                string
	QRCode                string
	Raw                   map[string]any
}

type webhookResult struct {
	EventKey        string
	ProviderTradeID string
	MerchantOrderNo string
	Paid            bool
	Raw             map[string]any
}

func (s *Service) loadChannel(ctx context.Context, token string) (channelRecord, error) {
	var channel channelRecord
	var ciphertext string
	err := s.db.QueryRow(ctx, `SELECT id,account_id,provider,display_name,enabled,config_ciphertext,webhook_token FROM payment_channels WHERE webhook_token=$1`, token).Scan(&channel.ID, &channel.AccountID, &channel.Provider, &channel.DisplayName, &channel.Enabled, &ciphertext, &channel.WebhookToken)
	if err != nil {
		return channel, err
	}
	plain, err := s.decrypt(ciphertext)
	if err != nil {
		return channel, err
	}
	if plain != "" {
		if err := json.Unmarshal([]byte(plain), &channel.Config); err != nil {
			return channel, fmt.Errorf("stored channel config is malformed: %w", err)
		}
	}
	return channel, nil
}

func (s *Service) createPayment(ctx context.Context, channel channelRecord, bill billRecord) (providerResult, error) {
	if !channel.Enabled {
		return providerResult{}, errors.New("payment channel is disabled")
	}
	switch channel.Provider {
	case "alipay":
		return s.alipayCreate(ctx, channel, bill)
	case "wechat":
		return s.wechatCreate(ctx, channel, bill)
	default:
		return providerResult{}, errors.New("unsupported provider")
	}
}

func (s *Service) refundPayment(ctx context.Context, channel channelRecord, bill billRecord, refund refundRecord) (string, map[string]any, error) {
	switch channel.Provider {
	case "alipay":
		return s.alipayRefund(ctx, channel, bill, refund)
	case "wechat":
		return s.wechatRefund(ctx, channel, bill, refund)
	default:
		return "", nil, errors.New("unsupported provider")
	}
}

func (s *Service) closePayment(ctx context.Context, channel channelRecord, bill billRecord) error {
	switch channel.Provider {
	case "alipay":
		return s.alipayClose(ctx, channel, bill)
	case "wechat":
		return s.wechatClose(ctx, channel, bill)
	default:
		return errors.New("unsupported provider")
	}
}

func (s *Service) verifyWebhook(channel channelRecord, headers http.Header, body []byte, form url.Values) (webhookResult, error) {
	switch channel.Provider {
	case "alipay":
		return s.verifyAlipayWebhook(channel, form)
	case "wechat":
		return s.verifyWechatWebhook(channel, headers, body)
	default:
		return webhookResult{}, errors.New("unsupported provider")
	}
}

func (s *Service) alipayCreate(ctx context.Context, channel channelRecord, bill billRecord) (providerResult, error) {
	cfg := channel.Config.Alipay
	if cfg == nil || cfg.AppID == "" || cfg.AppPrivateKeyPEM == "" {
		return providerResult{}, errors.New("支付宝通道未完成 app_id 或应用私钥配置")
	}
	method := "alipay.trade.page.pay"
	if bill.Scene == "wap" {
		method = "alipay.trade.wap.pay"
	}
	if bill.Scene != "page" && bill.Scene != "wap" {
		return providerResult{}, errors.New("支付宝仅支持 page 或 wap 场景")
	}
	biz := map[string]any{"out_trade_no": bill.MerchantOrderNo, "product_code": "FAST_INSTANT_TRADE_PAY", "total_amount": moneyString(bill.AmountMinor), "subject": bill.Subject}
	if bill.Scene == "wap" {
		biz["product_code"] = "QUICK_WAP_WAY"
		if bill.ExpiresAt != nil {
			biz["time_expire"] = bill.ExpiresAt.Format("2006-01-02 15:04:05")
		}
	}
	bizBytes, _ := json.Marshal(biz)
	params := url.Values{"app_id": {cfg.AppID}, "method": {method}, "format": {"JSON"}, "charset": {"utf-8"}, "sign_type": {"RSA2"}, "timestamp": {time.Now().Format("2006-01-02 15:04:05")}, "version": {"1.0"}, "notify_url": {fmt.Sprintf("%s/api/v1/webhooks/alipay/%s", s.baseURL, channel.WebhookToken)}, "biz_content": {string(bizBytes)}}
	if bill.ReturnURL != "" {
		params.Set("return_url", bill.ReturnURL)
	} else if cfg.ReturnURL != "" {
		params.Set("return_url", cfg.ReturnURL)
	}
	sign, err := rsaSign(cfg.AppPrivateKeyPEM, canonicalValues(params))
	if err != nil {
		return providerResult{}, err
	}
	params.Set("sign", sign)
	gateway := cfg.GatewayURL
	if gateway == "" {
		gateway = "https://openapi.alipay.com/gateway.do"
	}
	return providerResult{PayURL: gateway + "?" + params.Encode(), Raw: map[string]any{"method": method, "gateway_url": gateway}}, nil
}

func (s *Service) alipayRefund(ctx context.Context, channel channelRecord, bill billRecord, refund refundRecord) (string, map[string]any, error) {
	cfg := channel.Config.Alipay
	if cfg == nil {
		return "", nil, errors.New("支付宝通道未配置")
	}
	biz, _ := json.Marshal(map[string]any{"out_trade_no": bill.MerchantOrderNo, "refund_amount": moneyString(refund.AmountMinor), "out_request_no": refund.RefundOrderNo, "refund_reason": refund.Reason})
	payload, err := s.alipayCall(ctx, cfg, "alipay.trade.refund", string(biz))
	if err != nil {
		return "", nil, err
	}
	return jsonString(payload, "trade_no"), payload, nil
}
func (s *Service) alipayClose(ctx context.Context, channel channelRecord, bill billRecord) error {
	cfg := channel.Config.Alipay
	if cfg == nil {
		return errors.New("支付宝通道未配置")
	}
	biz, _ := json.Marshal(map[string]any{"out_trade_no": bill.MerchantOrderNo})
	_, err := s.alipayCall(ctx, cfg, "alipay.trade.close", string(biz))
	return err
}
func (s *Service) alipayCall(ctx context.Context, cfg *alipayConfig, method, biz string) (map[string]any, error) {
	params := url.Values{"app_id": {cfg.AppID}, "method": {method}, "format": {"JSON"}, "charset": {"utf-8"}, "sign_type": {"RSA2"}, "timestamp": {time.Now().Format("2006-01-02 15:04:05")}, "version": {"1.0"}, "biz_content": {biz}}
	sign, err := rsaSign(cfg.AppPrivateKeyPEM, canonicalValues(params))
	if err != nil {
		return nil, err
	}
	params.Set("sign", sign)
	gateway := cfg.GatewayURL
	if gateway == "" {
		gateway = "https://openapi.alipay.com/gateway.do"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("支付宝接口响应 %d", resp.StatusCode)
	}
	var outer map[string]any
	if err := json.Unmarshal(body, &outer); err != nil {
		return nil, err
	}
	for key, value := range outer {
		if strings.HasSuffix(key, "_response") {
			if data, ok := value.(map[string]any); ok {
				if code, _ := data["code"].(string); code != "10000" {
					return nil, fmt.Errorf("支付宝接口错误: %v", data["sub_msg"])
				}
				return data, nil
			}
		}
	}
	return nil, errors.New("支付宝响应格式错误")
}
func (s *Service) verifyAlipayWebhook(channel channelRecord, form url.Values) (webhookResult, error) {
	cfg := channel.Config.Alipay
	if cfg == nil || cfg.AlipayPublicKeyPEM == "" {
		return webhookResult{}, errors.New("支付宝公钥未配置")
	}
	sign := form.Get("sign")
	if sign == "" {
		return webhookResult{}, errors.New("missing sign")
	}
	values := url.Values{}
	for key, raw := range form {
		if key != "sign" && key != "sign_type" && len(raw) > 0 && raw[0] != "" {
			values[key] = raw
		}
	}
	if err := rsaVerify(cfg.AlipayPublicKeyPEM, canonicalValues(values), sign); err != nil {
		return webhookResult{}, err
	}
	raw := map[string]any{}
	for key, values := range form {
		raw[key] = values[0]
	}
	return webhookResult{EventKey: "alipay:" + form.Get("notify_id") + ":" + form.Get("trade_no"), ProviderTradeID: form.Get("trade_no"), MerchantOrderNo: form.Get("out_trade_no"), Paid: form.Get("trade_status") == "TRADE_SUCCESS" || form.Get("trade_status") == "TRADE_FINISHED", Raw: raw}, nil
}

func (s *Service) wechatCreate(ctx context.Context, channel channelRecord, bill billRecord) (providerResult, error) {
	cfg := channel.Config.Wechat
	if cfg == nil || cfg.MchID == "" || cfg.AppID == "" || cfg.MerchantPrivateKeyPEM == "" {
		return providerResult{}, errors.New("微信支付通道未完成商户号、应用 ID 或商户私钥配置")
	}
	path := "/v3/pay/transactions/native"
	body := map[string]any{"appid": cfg.AppID, "mchid": cfg.MchID, "description": bill.Subject, "out_trade_no": bill.MerchantOrderNo, "notify_url": fmt.Sprintf("%s/api/v1/webhooks/wechat/%s", s.baseURL, channel.WebhookToken), "amount": map[string]any{"total": bill.AmountMinor, "currency": "CNY"}}
	switch bill.Scene {
	case "native":
	case "jsapi":
		path = "/v3/pay/transactions/jsapi"
		return providerResult{}, errors.New("JSAPI 场景需要由商户业务传入 openid，当前 OPS API 未暴露该字段")
	case "wap":
		path = "/v3/pay/transactions/h5"
		body["scene_info"] = map[string]any{"payer_client_ip": "127.0.0.1", "h5_info": map[string]string{"type": "Wap"}}
	default:
		return providerResult{}, errors.New("微信支付支持 native 或 wap 场景")
	}
	if bill.ExpiresAt != nil {
		body["time_expire"] = bill.ExpiresAt.Format(time.RFC3339)
	}
	payload, _ := json.Marshal(body)
	response, err := s.wechatCall(ctx, cfg, http.MethodPost, path, payload)
	if err != nil {
		return providerResult{}, err
	}
	var data map[string]any
	if err := json.Unmarshal(response, &data); err != nil {
		return providerResult{}, err
	}
	result := providerResult{Raw: data}
	if codeURL, _ := data["code_url"].(string); codeURL != "" {
		result.QRCode = codeURL
	}
	if h5URL, _ := data["h5_url"].(string); h5URL != "" {
		result.PayURL = h5URL
	}
	if prepay, _ := data["prepay_id"].(string); prepay != "" {
		result.ProviderTransactionID = prepay
	}
	return result, nil
}
func (s *Service) wechatRefund(ctx context.Context, channel channelRecord, bill billRecord, refund refundRecord) (string, map[string]any, error) {
	cfg := channel.Config.Wechat
	if cfg == nil {
		return "", nil, errors.New("微信支付通道未配置")
	}
	body, _ := json.Marshal(map[string]any{"out_trade_no": bill.MerchantOrderNo, "out_refund_no": refund.RefundOrderNo, "reason": refund.Reason, "amount": map[string]any{"refund": refund.AmountMinor, "total": bill.AmountMinor, "currency": "CNY"}, "notify_url": fmt.Sprintf("%s/api/v1/webhooks/wechat/%s", s.baseURL, channel.WebhookToken)})
	response, err := s.wechatCall(ctx, cfg, http.MethodPost, "/v3/refund/domestic/refunds", body)
	if err != nil {
		return "", nil, err
	}
	var data map[string]any
	if err = json.Unmarshal(response, &data); err != nil {
		return "", nil, err
	}
	id, _ := data["refund_id"].(string)
	return id, data, nil
}
func (s *Service) wechatClose(ctx context.Context, channel channelRecord, bill billRecord) error {
	cfg := channel.Config.Wechat
	if cfg == nil {
		return errors.New("微信支付通道未配置")
	}
	_, err := s.wechatCall(ctx, cfg, http.MethodPost, "/v3/pay/transactions/out-trade-no/"+url.PathEscape(bill.MerchantOrderNo)+"/close", []byte(`{"mchid":"`+cfg.MchID+`"}`))
	return err
}
func (s *Service) wechatCall(ctx context.Context, cfg *wechatConfig, method, path string, body []byte) ([]byte, error) {
	timestamp := fmt.Sprint(time.Now().Unix())
	nonce := randomToken(16)
	message := method + "\n" + path + "\n" + timestamp + "\n" + nonce + "\n" + string(body) + "\n"
	sign, err := rsaSignBytes(cfg.MerchantPrivateKeyPEM, []byte(message))
	if err != nil {
		return nil, err
	}
	auth := fmt.Sprintf(`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`, cfg.MchID, nonce, timestamp, cfg.MerchantSerialNo, sign)
	req, err := http.NewRequestWithContext(ctx, method, "https://api.mch.weixin.qq.com"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("微信支付接口响应 %d: %s", resp.StatusCode, strings.TrimSpace(string(response)))
	}
	return response, nil
}
func (s *Service) verifyWechatWebhook(channel channelRecord, headers http.Header, body []byte) (webhookResult, error) {
	cfg := channel.Config.Wechat
	if cfg == nil || cfg.PlatformPublicKeyPEM == "" || cfg.APIv3Key == "" {
		return webhookResult{}, errors.New("微信平台公钥或 APIv3 密钥未配置")
	}
	timestamp := headers.Get("Wechatpay-Timestamp")
	nonce := headers.Get("Wechatpay-Nonce")
	signature := headers.Get("Wechatpay-Signature")
	if timestamp == "" || nonce == "" || signature == "" {
		return webhookResult{}, errors.New("missing Wechatpay signature headers")
	}
	message := timestamp + "\n" + nonce + "\n" + string(body) + "\n"
	if err := rsaVerifyBytes(cfg.PlatformPublicKeyPEM, []byte(message), signature); err != nil {
		return webhookResult{}, err
	}
	var envelope struct {
		ID       string `json:"id"`
		Resource struct {
			Ciphertext     string `json:"ciphertext"`
			Nonce          string `json:"nonce"`
			AssociatedData string `json:"associated_data"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return webhookResult{}, err
	}
	plaintext, err := wechatDecrypt(cfg.APIv3Key, envelope.Resource.Nonce, envelope.Resource.AssociatedData, envelope.Resource.Ciphertext)
	if err != nil {
		return webhookResult{}, err
	}
	var raw map[string]any
	if err = json.Unmarshal(plaintext, &raw); err != nil {
		return webhookResult{}, err
	}
	orderNo, _ := raw["out_trade_no"].(string)
	tradeID, _ := raw["transaction_id"].(string)
	state, _ := raw["trade_state"].(string)
	return webhookResult{EventKey: "wechat:" + envelope.ID, ProviderTradeID: tradeID, MerchantOrderNo: orderNo, Paid: state == "SUCCESS", Raw: raw}, nil
}

func canonicalValues(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := values.Get(key); value != "" {
			pairs = append(pairs, key+"="+value)
		}
	}
	return strings.Join(pairs, "&")
}
func rsaSign(pemText, message string) (string, error) { return rsaSignBytes(pemText, []byte(message)) }
func rsaSignBytes(pemText string, message []byte) (string, error) {
	key, err := parsePrivateKey(pemText)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(message)
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}
func rsaVerify(pemText, message, signature string) error {
	return rsaVerifyBytes(pemText, []byte(message), signature)
}
func rsaVerifyBytes(pemText string, message []byte, signature string) error {
	key, err := parsePublicKey(pemText)
	if err != nil {
		return err
	}
	signed, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(message)
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signed)
}
func parsePrivateKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("invalid RSA private key PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("PEM does not contain an RSA private key")
	}
	return key, nil
}
func parsePublicKey(pemText string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("invalid RSA public key PEM")
	}
	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := keyAny.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("PEM does not contain an RSA public key")
	}
	return key, nil
}
func wechatDecrypt(apiKey, nonce, associated, cipherText string) ([]byte, error) {
	if len(apiKey) != 32 {
		return nil, errors.New("微信 APIv3 密钥必须为 32 字节")
	}
	payload, err := base64.StdEncoding.DecodeString(cipherText)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher([]byte(apiKey))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, []byte(nonce), payload, []byte(associated))
}
func jsonString(data map[string]any, key string) string { value, _ := data[key].(string); return value }
