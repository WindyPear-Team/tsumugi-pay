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
	"strconv"
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
	PID                string `json:"pid"`
	Mode               string `json:"mode"`
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

type providerPaymentStatus struct {
	Paid          bool
	TransactionID string
	Raw           map[string]any
}

type webhookResult struct {
	EventKey        string
	ProviderTradeID string
	MerchantOrderNo string
	Paid            bool
	Raw             map[string]any
}

func (s *Service) loadChannel(ctx context.Context, token string) (channelRecord, error) {
	var model PaymentChannel
	err := s.db.DB().WithContext(ctx).Where("webhook_token = ?", token).Take(&model).Error
	if err != nil {
		return channelRecord{}, err
	}
	channel := channelRecord{ID: model.ID, AccountID: model.AccountID, Provider: model.Provider, DisplayName: model.DisplayName, Enabled: model.Enabled, WebhookToken: model.WebhookToken}
	plain, err := s.decrypt(model.ConfigCiphertext)
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

func (s *Service) queryPaymentStatus(ctx context.Context, channel channelRecord, bill billRecord) (providerPaymentStatus, error) {
	switch channel.Provider {
	case "alipay":
		return s.alipayQuery(ctx, channel, bill)
	case "wechat":
		return s.wechatQuery(ctx, channel, bill)
	default:
		return providerPaymentStatus{}, errors.New("unsupported provider")
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
	if cfg == nil || cfg.PID == "" || cfg.AppID == "" || cfg.AppPrivateKeyPEM == "" {
		return providerResult{}, errors.New("支付宝通道未完成 pid、app_id 或应用私钥配置")
	}
	switch alipayPaymentMode(cfg) {
	case "face_to_face":
		if bill.Scene != "native" {
			return providerResult{}, errors.New("支付宝当面付只支持 native 二维码场景")
		}
		return s.alipayPrecreate(ctx, cfg, channel, bill)
	case "bill_query":
		if bill.Scene != "native" {
			return providerResult{}, errors.New("支付宝账单查询支付只支持 native 二维码场景")
		}
		return s.alipayBillQueryCreate(cfg, bill)
	case "website":
		if bill.Scene != "page" && bill.Scene != "wap" {
			return providerResult{}, errors.New("支付宝网站支付只支持 page 或 wap 场景")
		}
	default:
		return providerResult{}, errors.New("支付宝支付方式必须为当面付或网站支付")
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
	return providerResult{PayURL: appendQuery(gateway, params), Raw: map[string]any{"method": method, "gateway_url": gateway}}, nil
}

func (s *Service) alipayPrecreate(ctx context.Context, cfg *alipayConfig, channel channelRecord, bill billRecord) (providerResult, error) {
	biz := map[string]any{
		"out_trade_no": bill.MerchantOrderNo,
		"total_amount": moneyString(bill.AmountMinor),
		"subject":      bill.Subject,
	}
	if bill.Description != "" {
		biz["body"] = bill.Description
	}
	if bill.ExpiresAt != nil {
		biz["timeout_express"] = strconv.Itoa(int(time.Until(*bill.ExpiresAt).Minutes())) + "m"
	}
	payload, err := json.Marshal(biz)
	if err != nil {
		return providerResult{}, err
	}
	notifyURL := fmt.Sprintf("%s/api/v1/webhooks/alipay/%s", s.baseURL, channel.WebhookToken)
	response, err := s.alipayCall(ctx, cfg, "alipay.trade.precreate", string(payload), notifyURL)
	if err != nil {
		return providerResult{}, err
	}
	qrCode, _ := response["qr_code"].(string)
	if qrCode == "" {
		return providerResult{}, errors.New("支付宝预创建订单未返回二维码地址")
	}
	return providerResult{ProviderTransactionID: jsonString(response, "trade_no"), QRCode: qrCode, PayURL: "alipayqr://platformapi/startapp?saId=10000007&qrcode=" + qrCode, Raw: response}, nil
}

func (s *Service) alipayRefund(ctx context.Context, channel channelRecord, bill billRecord, refund refundRecord) (string, map[string]any, error) {
	cfg := channel.Config.Alipay
	if cfg == nil {
		return "", nil, errors.New("支付宝通道未配置")
	}
	biz, _ := json.Marshal(map[string]any{"out_trade_no": bill.MerchantOrderNo, "refund_amount": moneyString(refund.AmountMinor), "out_request_no": refund.RefundOrderNo, "refund_reason": refund.Reason})
	payload, err := s.alipayCall(ctx, cfg, "alipay.trade.refund", string(biz), "")
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
	_, err := s.alipayCall(ctx, cfg, "alipay.trade.close", string(biz), "")
	return err
}
func (s *Service) alipayQuery(ctx context.Context, channel channelRecord, bill billRecord) (providerPaymentStatus, error) {
	cfg := channel.Config.Alipay
	if cfg == nil {
		return providerPaymentStatus{}, errors.New("支付宝通道未配置")
	}
	if alipayPaymentMode(cfg) == "bill_query" {
		return s.alipayAccountLogQuery(ctx, cfg, bill)
	}
	biz, _ := json.Marshal(map[string]string{"out_trade_no": bill.MerchantOrderNo})
	response, err := s.alipayCall(ctx, cfg, "alipay.trade.query", string(biz), "")
	if err != nil {
		return providerPaymentStatus{}, err
	}
	tradeID, _ := response["trade_no"].(string)
	tradeStatus, _ := response["trade_status"].(string)
	return providerPaymentStatus{Paid: tradeStatus == "TRADE_SUCCESS" || tradeStatus == "TRADE_FINISHED", TransactionID: tradeID, Raw: response}, nil
}

func (s *Service) alipayBillQueryCreate(cfg *alipayConfig, bill billRecord) (providerResult, error) {
	if cfg.PID == "" {
		return providerResult{}, errors.New("支付宝账单查询支付需要配置 pid")
	}
	biz, err := json.Marshal(map[string]string{
		"s": "money",
		"u": cfg.PID,
		"a": moneyString(bill.AmountMinor),
		"m": bill.MerchantOrderNo,
	})
	if err != nil {
		return providerResult{}, err
	}
	inner := "alipays://platformapi/startapp?appId=20000123&actionType=scan&biz_data=" + url.QueryEscape(string(biz))
	paymentURL := "alipayqr://platformapi/startapp?saId=20000032&url=" + url.QueryEscape(inner)
	baseURL := strings.TrimRight(s.baseURL, "/")
	if baseURL == "" || bill.PlatformOrderNo == "" {
		return providerResult{}, errors.New("支付宝账单查询支付需要平台订单地址")
	}
	landingURL := baseURL + "/api/v1/payments/" + url.PathEscape(bill.PlatformOrderNo) + "/alipay"
	return providerResult{QRCode: landingURL, PayURL: paymentURL, Raw: map[string]any{"method": "alipay.data.bill.accountlog.query", "reconciliation": "merchant_order_no"}}, nil
}

func (s *Service) alipayAccountLogQuery(ctx context.Context, cfg *alipayConfig, bill billRecord) (providerPaymentStatus, error) {
	if cfg.AlipayPublicKeyPEM == "" {
		return providerPaymentStatus{}, errors.New("支付宝账单查询需要配置支付宝公钥")
	}
	now := time.Now()
	start := bill.CreatedAt
	if start.IsZero() || start.After(now) {
		start = now.Add(-15 * time.Minute)
	} else {
		start = start.Add(-2 * time.Minute)
	}
	biz, err := json.Marshal(map[string]string{
		"start_time": start.Format("2006-01-02 15:04:05"),
		"end_time":   now.Format("2006-01-02 15:04:05"),
		"page_no":    "1",
		"page_size":  "100",
	})
	if err != nil {
		return providerPaymentStatus{}, err
	}
	params := url.Values{"app_id": {cfg.AppID}, "method": {"alipay.data.bill.accountlog.query"}, "format": {"JSON"}, "charset": {"utf-8"}, "sign_type": {"RSA2"}, "timestamp": {time.Now().Format("2006-01-02 15:04:05")}, "version": {"1.0"}, "biz_content": {string(biz)}}
	sign, err := rsaSign(cfg.AppPrivateKeyPEM, canonicalValues(params))
	if err != nil {
		return providerPaymentStatus{}, err
	}
	params.Set("sign", sign)
	gateway := cfg.GatewayURL
	if gateway == "" {
		gateway = "https://openapi.alipay.com/gateway.do"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway, strings.NewReader(params.Encode()))
	if err != nil {
		return providerPaymentStatus{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return providerPaymentStatus{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return providerPaymentStatus{}, err
	}
	if resp.StatusCode/100 != 2 {
		return providerPaymentStatus{}, fmt.Errorf("支付宝账单查询接口响应 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(body, &outer); err != nil {
		return providerPaymentStatus{}, fmt.Errorf("解析支付宝账单查询响应: %w", err)
	}
	responseJSON, ok := outer["alipay_data_bill_accountlog_query_response"]
	if !ok {
		return providerPaymentStatus{}, errors.New("支付宝账单查询响应缺少业务数据")
	}
	var responseSignature string
	if err := json.Unmarshal(outer["sign"], &responseSignature); err != nil || responseSignature == "" {
		return providerPaymentStatus{}, errors.New("支付宝账单查询响应缺少签名")
	}
	if err := rsaVerifyBytes(cfg.AlipayPublicKeyPEM, responseJSON, responseSignature); err != nil {
		return providerPaymentStatus{}, fmt.Errorf("支付宝账单查询响应验签失败: %w", err)
	}
	var response struct {
		Code       string           `json:"code"`
		SubMsg     string           `json:"sub_msg"`
		DetailList []map[string]any `json:"detail_list"`
	}
	if err := json.Unmarshal(responseJSON, &response); err != nil {
		return providerPaymentStatus{}, fmt.Errorf("解析支付宝账单明细: %w", err)
	}
	if response.Code != "10000" {
		return providerPaymentStatus{}, fmt.Errorf("支付宝账单查询接口错误: %s", response.SubMsg)
	}
	for _, detail := range response.DetailList {
		if !alipayOrderReferenceMatches(detail, bill.MerchantOrderNo) || !alipayIncoming(detail) || !alipayAmountMatches(detail, bill.AmountMinor) {
			continue
		}
		tradeID := jsonString(detail, "alipay_order_no")
		if tradeID == "" {
			tradeID = jsonString(detail, "account_log_id")
		}
		if tradeID == "" {
			continue
		}
		return providerPaymentStatus{Paid: true, TransactionID: tradeID, Raw: map[string]any{"verified": true, "method": "alipay.data.bill.accountlog.query", "detail": detail}}, nil
	}
	return providerPaymentStatus{Raw: map[string]any{"verified": true, "method": "alipay.data.bill.accountlog.query", "detail_count": len(response.DetailList)}}, nil
}

func alipayOrderReferenceMatches(detail map[string]any, orderNo string) bool {
	for _, key := range []string{"merchant_order_no", "memo", "trans_memo", "remark"} {
		if strings.TrimSpace(jsonString(detail, key)) == orderNo {
			return true
		}
	}
	return false
}

func alipayIncoming(detail map[string]any) bool {
	switch strings.ToUpper(strings.TrimSpace(jsonString(detail, "direction"))) {
	case "IN", "INCOME", "CREDIT", "收入":
		return true
	default:
		return false
	}
}

func alipayAmountMatches(detail map[string]any, amountMinor int64) bool {
	for _, key := range []string{"trans_amount", "amount"} {
		if value := jsonString(detail, key); value != "" {
			parsed, err := parseAmount(value)
			return err == nil && parsed == amountMinor
		}
	}
	return false
}
func (s *Service) alipayCall(ctx context.Context, cfg *alipayConfig, method, biz, notifyURL string) (map[string]any, error) {
	params := url.Values{"app_id": {cfg.AppID}, "method": {method}, "format": {"JSON"}, "charset": {"utf-8"}, "sign_type": {"RSA2"}, "timestamp": {time.Now().Format("2006-01-02 15:04:05")}, "version": {"1.0"}, "biz_content": {biz}}
	if notifyURL != "" {
		params.Set("notify_url", notifyURL)
	}
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

func alipayPaymentMode(cfg *alipayConfig) string {
	if cfg == nil || cfg.Mode == "" {
		return "face_to_face"
	}
	return cfg.Mode
}

func appendQuery(base string, values url.Values) string {
	separator := "?"
	if strings.Contains(base, "?") {
		separator = "&"
	}
	return base + separator + values.Encode()
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
func (s *Service) wechatQuery(ctx context.Context, channel channelRecord, bill billRecord) (providerPaymentStatus, error) {
	cfg := channel.Config.Wechat
	if cfg == nil {
		return providerPaymentStatus{}, errors.New("微信支付通道未配置")
	}
	path := "/v3/pay/transactions/out-trade-no/" + url.PathEscape(bill.MerchantOrderNo) + "?mchid=" + url.QueryEscape(cfg.MchID)
	response, err := s.wechatCall(ctx, cfg, http.MethodGet, path, nil)
	if err != nil {
		return providerPaymentStatus{}, err
	}
	var data map[string]any
	if err := json.Unmarshal(response, &data); err != nil {
		return providerPaymentStatus{}, err
	}
	tradeID, _ := data["transaction_id"].(string)
	tradeState, _ := data["trade_state"].(string)
	return providerPaymentStatus{Paid: tradeState == "SUCCESS", TransactionID: tradeID, Raw: data}, nil
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

// fetchWechatPlatformCertificate obtains the active platform certificate with
// the merchant's signed API request, then stores its public key for webhook
// signature verification. The certificate payload is encrypted with APIv3Key.
func (s *Service) fetchWechatPlatformCertificate(ctx context.Context, cfg *wechatConfig) error {
	response, err := s.wechatCall(ctx, cfg, http.MethodGet, "/v3/certificates", nil)
	if err != nil {
		return err
	}
	var payload struct {
		Certificates []struct {
			SerialNo           string    `json:"serial_no"`
			EffectiveTime      time.Time `json:"effective_time"`
			ExpireTime         time.Time `json:"expire_time"`
			EncryptCertificate struct {
				Algorithm      string `json:"algorithm"`
				Nonce          string `json:"nonce"`
				AssociatedData string `json:"associated_data"`
				Ciphertext     string `json:"ciphertext"`
			} `json:"encrypt_certificate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &payload); err != nil {
		return fmt.Errorf("decode platform certificates: %w", err)
	}
	now := time.Now()
	for _, item := range payload.Certificates {
		if item.EncryptCertificate.Algorithm != "AEAD_AES_256_GCM" || item.SerialNo == "" || now.Before(item.EffectiveTime) || !now.Before(item.ExpireTime) {
			continue
		}
		certificatePEM, err := wechatDecrypt(cfg.APIv3Key, item.EncryptCertificate.Nonce, item.EncryptCertificate.AssociatedData, item.EncryptCertificate.Ciphertext)
		if err != nil {
			return fmt.Errorf("decrypt platform certificate: %w", err)
		}
		block, _ := pem.Decode(certificatePEM)
		if block == nil {
			return errors.New("platform certificate is not PEM")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("parse platform certificate: %w", err)
		}
		publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
		if !ok {
			return errors.New("platform certificate does not contain an RSA public key")
		}
		der, err := x509.MarshalPKIXPublicKey(publicKey)
		if err != nil {
			return fmt.Errorf("encode platform public key: %w", err)
		}
		cfg.PlatformPublicKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
		cfg.PlatformSerialNo = item.SerialNo
		return nil
	}
	return errors.New("no active WeChat Pay platform certificate returned")
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
	if keyID := headers.Get("Wechatpay-Serial"); cfg.PlatformSerialNo != "" && keyID != "" && !strings.EqualFold(keyID, cfg.PlatformSerialNo) {
		return webhookResult{}, errors.New("Wechatpay platform certificate or public key ID does not match configured value")
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
	der, err := decodeRSAKey(pemText, "private")
	if err != nil {
		return nil, err
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(der)
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
	der, err := decodeRSAKey(pemText, "public")
	if err != nil {
		return nil, err
	}
	if key, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	key, ok := keyAny.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("PEM does not contain an RSA public key")
	}
	return key, nil
}

func decodeRSAKey(value, kind string) ([]byte, error) {
	if block, _ := pem.Decode([]byte(strings.TrimSpace(value))); block != nil {
		return block.Bytes, nil
	}
	// Alipay's key-management page commonly displays a bare Base64 key instead
	// of a PEM block. Whitespace is ignored so copied line-wrapped keys work too.
	compact := strings.NewReplacer(" ", "", "\n", "", "\r", "", "\t", "").Replace(value)
	der, err := base64.StdEncoding.DecodeString(compact)
	if err != nil || len(der) == 0 {
		return nil, fmt.Errorf("invalid RSA %s key; paste a PEM block or Base64 key", kind)
	}
	return der, nil
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
func jsonString(data map[string]any, key string) string {
	switch value := data[key].(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return ""
	}
}
