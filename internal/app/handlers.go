package app

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type accountRecord struct {
	ID                                                  uuid.UUID
	Name, MerchantNo, Status, APISecret, CallbackSecret string
	CreatedAt                                           time.Time
}
type billRecord struct {
	ID, AccountID, ChannelID                               uuid.UUID
	PlatformOrderNo, MerchantOrderNo, Subject, Description string
	AmountMinor                                            int64
	Currency, Provider, Scene, Status                      string
	ProviderTransactionID                                  string
	NotifyURL, ReturnURL, Metadata                         string
	ExpiresAt, PaidAt, ClosedAt                            *time.Time
	CreatedAt, UpdatedAt                                   time.Time
}
type refundRecord struct {
	ID, AccountID, BillID            uuid.UUID
	RefundOrderNo                    string
	AmountMinor                      int64
	Reason, Status, ProviderRefundID string
	CreatedAt                        time.Time
}
type smtpSiteConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
}
type hcaptchaSiteConfig struct {
	Enabled   bool   `json:"enabled"`
	SiteKey   string `json:"site_key"`
	SecretKey string `json:"secret_key"`
}
type oidcSiteConfig struct {
	Enabled               bool   `json:"enabled"`
	IssuerURL             string `json:"issuer_url"`
	ClientID              string `json:"client_id"`
	ClientSecret          string `json:"client_secret"`
	RedirectURL           string `json:"redirect_url"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	LoginLabel            string `json:"login_label"`
}
type accountSiteSettings struct {
	Email    smtpSiteConfig
	HCaptcha hcaptchaSiteConfig
	OIDC     oidcSiteConfig
}
type siteConfig struct {
	SiteName                  string   `json:"site_name"`
	AllowPasswordLogin        bool     `json:"allow_password_login"`
	AllowPasswordRegistration bool     `json:"allow_password_registration"`
	EmailWhitelist            []string `json:"email_whitelist"`
	TermsURL                  string   `json:"terms_url"`
	PrivacyPolicyURL          string   `json:"privacy_policy_url"`
	FaviconURL                string   `json:"favicon_url"`
	ThemeColor                string   `json:"theme_color"`
}
type paymentInput struct {
	MerchantID      string `json:"merchant_id"`
	PaymentMethod   string `json:"payment_method"`
	MerchantOrderNo string `json:"merchant_order_no"`
	Subject         string `json:"subject"`
	Description     string `json:"description"`
	Amount          string `json:"amount"`
	NotifyURL       string `json:"notify_url"`
	ReturnURL       string `json:"return_url"`
	Metadata        string `json:"metadata"`
	Scene           string `json:"scene"`
	SignType        string `json:"sign_type"`
	Sign            string `json:"sign"`
	signatureValues url.Values
}
type paymentResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		PlatformOrderNo string     `json:"platform_order_no"`
		MerchantOrderNo string     `json:"merchant_order_no"`
		PayURL          string     `json:"pay_url,omitempty"`
		QRCode          string     `json:"qrcode,omitempty"`
		CheckoutURL     string     `json:"checkout_url,omitempty"`
		ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	} `json:"data"`
}

func (s *Service) discovery(w http.ResponseWriter, r *http.Request) {
	base := s.baseURL
	writeJSON(w, http.StatusOK, map[string]any{
		"spec": "Open Payment Specification", "spec_version": "1.0.0", "profile": []string{"OPS-EPAY-1", "OPS-CORE-1", "OPS-EXT-1"},
		"platform":        map[string]any{"name": "Tsumugi Pay", "vendor": "tsumugi", "homepage": base, "charset": "utf-8", "timezone": "Asia/Shanghai", "currency": "CNY"},
		"endpoints":       map[string]any{"submit": base + "/submit.php", "mapi": base + "/mapi.php", "api": base + "/api.php", "query": base + "/api.php?act=order"},
		"transports":      map[string]any{"payment_create": []string{"form_get", "form_post", "json"}, "query": []string{"form_get", "form_post"}, "refund": []string{"json"}, "notify": []string{"form_post"}},
		"signing":         map[string]any{"default": "HMAC-SHA256", "supported": []string{"MD5", "HMAC-SHA256"}, "sign_field": "sign", "sign_type_field": "sign_type", "empty_value_policy": "omit", "sort": "ascii_asc", "charset": "utf-8"},
		"payment_methods": []map[string]any{{"code": "alipay", "name": "支付宝", "aliases": []string{"alipay", "ali"}, "scenes": []string{"pc", "wap"}, "enabled": true}, {"code": "wxpay", "name": "微信支付", "aliases": []string{"wxpay", "wechat"}, "scenes": []string{"pc", "wap", "qr", "jsapi"}, "enabled": true}},
		"fields":          map[string]any{"merchant_id": []string{"pid", "merchant_id"}, "payment_method": []string{"type", "payment_method"}, "merchant_order_no": []string{"out_trade_no", "merchant_order_no"}, "subject": []string{"name", "subject"}, "amount": []string{"money", "amount"}, "notify_url": []string{"notify_url"}, "return_url": []string{"return_url"}, "metadata": []string{"param", "metadata"}},
		"amount":          map[string]any{"currency": "CNY", "format": "decimal_string", "scale": 2, "min": "0.01"}, "callbacks": map[string]any{"notify_success_body": "success", "notify_retry": true, "return_url_trusted": false}, "security": map[string]any{"https_required": s.environment == "production", "replay_protection": false},
	})
}

func (s *Service) publicCreate(w http.ResponseWriter, r *http.Request) {
	var input paymentInput
	if !decodeJSON(w, r, &input) {
		return
	}
	s.processPublicCreate(w, r, input, false)
}

func (s *Service) publicCheckout(w http.ResponseWriter, r *http.Request) {
	orderNo := strings.TrimSpace(r.PathValue("orderNo"))
	if orderNo == "" || len(orderNo) > 64 {
		writeError(w, http.StatusBadRequest, 40002, "invalid payment order", requestID(r))
		return
	}
	var bill Bill
	if err := s.db.DB().WithContext(r.Context()).Take(&bill, "platform_order_no = ?", orderNo).Error; err != nil {
		writeError(w, http.StatusNotFound, 40401, "payment order not found", requestID(r))
		return
	}
	if bill.Status == "pending" && s.shouldPollBill(bill.ID) {
		if _, _, err := s.pollBill(r.Context(), billRecordFromModel(bill)); err != nil {
			s.logger.Warn("payment polling failed", "bill_id", bill.ID, "error", err)
		}
		_ = s.db.DB().WithContext(r.Context()).Take(&bill, "id = ?", bill.ID).Error
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(bill.ProviderPayload), &payload)
	qrcode, _ := payload["checkout_qrcode"].(string)
	if qrcode == "" {
		qrcode, _ = payload["code_url"].(string)
	}
	payURL, _ := payload["checkout_pay_url"].(string)
	if payURL == "" {
		payURL, _ = payload["h5_url"].(string)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"platform_order_no": bill.PlatformOrderNo, "merchant_order_no": bill.MerchantOrderNo, "subject": bill.Subject,
		"amount": moneyString(bill.AmountMinor), "currency": bill.Currency, "provider": bill.Provider, "status": bill.Status,
		"qrcode": qrcode, "pay_url": payURL, "return_url": stringValue(bill.ReturnURL), "expires_at": bill.ExpiresAt, "paid_at": bill.PaidAt,
	})
}

func (s *Service) legacyCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, 400, 40002, "invalid form", requestID(r))
		return
	}
	input := inputFromValues(r.Form)
	response, err := s.createBill(r.Context(), input)
	if err != nil {
		writePublicError(w, r, err)
		return
	}
	if response.Data.CheckoutURL != "" {
		http.Redirect(w, r, response.Data.CheckoutURL, http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
func (s *Service) processPublicCreate(w http.ResponseWriter, r *http.Request, input paymentInput, _ bool) {
	response, err := s.createBill(r.Context(), input)
	if err != nil {
		writePublicError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Service) createBill(ctx context.Context, input paymentInput) (paymentResponse, error) {
	input.normalize()
	if input.MerchantID == "" || input.PaymentMethod == "" || input.MerchantOrderNo == "" || input.Subject == "" || input.Amount == "" || input.NotifyURL == "" || input.Sign == "" {
		return paymentResponse{}, clientError{40002, "missing required payment fields"}
	}
	if len(input.MerchantOrderNo) > 128 || len(input.Subject) > 256 || len(input.Metadata) > 4096 {
		return paymentResponse{}, clientError{40002, "payment field exceeds length limit"}
	}
	if input.PaymentMethod == "ali" {
		input.PaymentMethod = "alipay"
	}
	if input.PaymentMethod == "wxpay" || input.PaymentMethod == "wechat" {
		input.PaymentMethod = "wechat"
	}
	if input.PaymentMethod != "alipay" && input.PaymentMethod != "wechat" {
		return paymentResponse{}, clientError{40003, "unsupported payment method"}
	}
	amount, err := parseAmount(input.Amount)
	if err != nil || amount < 1 {
		return paymentResponse{}, clientError{40004, "invalid amount"}
	}
	if !s.validCallbackURL(input.NotifyURL) {
		return paymentResponse{}, clientError{40002, "invalid notify_url"}
	}
	if input.ReturnURL != "" && !s.validCallbackURL(input.ReturnURL) {
		return paymentResponse{}, clientError{40002, "invalid return_url"}
	}
	account, err := s.accountByMerchantNo(ctx, input.MerchantID)
	if err != nil {
		if isNoRows(err) {
			return paymentResponse{}, clientError{40005, "merchant not found or disabled"}
		}
		return paymentResponse{}, err
	}
	if err := verifyOPSSignature(input, account.APISecret); err != nil {
		return paymentResponse{}, clientError{40001, "invalid signature"}
	}
	channelModel, err := s.selectChannel(ctx, account.ID, input.PaymentMethod)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return paymentResponse{}, clientError{40003, "no enabled payment channel for the selected payment method"}
		}
		s.logger.Error("cannot select payment channel", "merchant_order_no", input.MerchantOrderNo, "provider", input.PaymentMethod, "error", err)
		return paymentResponse{}, err
	}
	if !channelModel.Enabled {
		return paymentResponse{}, clientError{40003, "payment channel is disabled"}
	}
	plain, err := s.decrypt(channelModel.ConfigCiphertext)
	if err != nil {
		s.logger.Error("cannot read payment channel config", "channel_id", channelModel.ID, "error", err)
		return paymentResponse{}, providerError{cause: errors.New("payment channel credentials cannot be read")}
	}
	channel := channelRecord{ID: channelModel.ID, AccountID: account.ID, Provider: input.PaymentMethod, Enabled: channelModel.Enabled, DisplayName: channelModel.DisplayName, WebhookToken: channelModel.WebhookToken}
	if plain != "" {
		if err = json.Unmarshal([]byte(plain), &channel.Config); err != nil {
			s.logger.Error("payment channel config is invalid", "channel_id", channelModel.ID, "error", err)
			return paymentResponse{}, providerError{cause: errors.New("payment channel configuration is invalid")}
		}
	}
	scene := input.Scene
	if scene == "" {
		if input.PaymentMethod == "alipay" && alipayPaymentMode(channel.Config.Alipay) == "website" {
			scene = "page"
		} else {
			scene = "native"
		}
	}
	if scene == "qr" {
		scene = "native"
	}
	if scene != "page" && scene != "wap" && scene != "native" && scene != "jsapi" {
		return paymentResponse{}, clientError{40003, "unsupported payment scene"}
	}
	expires := nowUTC().Add(15 * time.Minute)
	bill := billRecord{ID: uuid.New(), AccountID: account.ID, ChannelID: channelModel.ID, PlatformOrderNo: "TP" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))[:24], MerchantOrderNo: input.MerchantOrderNo, Subject: input.Subject, Description: input.Description, AmountMinor: amount, Currency: "CNY", Provider: input.PaymentMethod, Scene: scene, Status: "pending", NotifyURL: input.NotifyURL, ReturnURL: input.ReturnURL, Metadata: input.Metadata, ExpiresAt: &expires, CreatedAt: nowUTC(), UpdatedAt: nowUTC()}
	err = s.db.DB().WithContext(ctx).Create(bill.toModel()).Error
	if err != nil {
		if isUniqueViolation(err) {
			return paymentResponse{}, clientError{40006, "duplicate merchant order number"}
		}
		s.logger.Error("cannot create payment bill", "merchant_order_no", input.MerchantOrderNo, "error", err)
		return paymentResponse{}, err
	}
	result, err := s.createPayment(ctx, channel, bill)
	if err != nil {
		_ = s.db.DB().WithContext(ctx).Model(&Bill{}).Where("id = ?", bill.ID).Update("status", "failed").Error
		s.logger.Error("payment channel request failed", "bill_id", bill.ID, "channel_id", channel.ID, "provider", channel.Provider, "error", err)
		return paymentResponse{}, providerError{cause: err}
	}
	payloadData := result.Raw
	if payloadData == nil {
		payloadData = map[string]any{}
	}
	if result.QRCode != "" {
		payloadData["checkout_qrcode"] = result.QRCode
	}
	if result.PayURL != "" {
		payloadData["checkout_pay_url"] = result.PayURL
	}
	payload, _ := json.Marshal(payloadData)
	err = s.db.DB().WithContext(ctx).Model(&Bill{}).Where("id = ?", bill.ID).Updates(map[string]any{"provider_transaction_id": nullableString(result.ProviderTransactionID), "provider_payload": string(payload)}).Error
	if err != nil {
		return paymentResponse{}, err
	}
	var response paymentResponse
	response.Code = 0
	response.Message = "success"
	response.Data.PlatformOrderNo = bill.PlatformOrderNo
	response.Data.MerchantOrderNo = bill.MerchantOrderNo
	response.Data.PayURL = result.PayURL
	response.Data.QRCode = result.QRCode
	response.Data.CheckoutURL = fmt.Sprintf("%s/checkout/%s", s.baseURL, bill.PlatformOrderNo)
	response.Data.ExpiresAt = bill.ExpiresAt
	return response, nil
}

// selectChannel selects among the best-priority enabled channels. Channels at
// the same priority are distributed according to their positive weight.
func (s *Service) selectChannel(ctx context.Context, accountID uuid.UUID, provider string) (PaymentChannel, error) {
	var candidates []PaymentChannel
	if err := s.db.DB().WithContext(ctx).Where("account_id = ? AND provider = ? AND enabled = ?", accountID, provider, true).Order("priority ASC, created_at ASC").Find(&candidates).Error; err != nil {
		return PaymentChannel{}, err
	}
	if len(candidates) == 0 {
		return PaymentChannel{}, gorm.ErrRecordNotFound
	}
	priority := candidates[0].Priority
	total := 0
	for _, channel := range candidates {
		if channel.Priority != priority {
			break
		}
		if channel.Weight > 0 {
			total += channel.Weight
		}
	}
	if total == 0 {
		return PaymentChannel{}, errors.New("no enabled payment channel has a positive weight")
	}
	pick, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(total)))
	if err != nil {
		return PaymentChannel{}, err
	}
	remaining := int(pick.Int64())
	for _, channel := range candidates {
		if channel.Priority != priority {
			break
		}
		if channel.Weight > 0 {
			remaining -= channel.Weight
			if remaining < 0 {
				return channel, nil
			}
		}
	}
	return PaymentChannel{}, errors.New("payment channel selection failed")
}

func (s *Service) legacyAPI(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, 400, 40002, "invalid request", requestID(r))
		return
	}
	act := r.Form.Get("act")
	switch act {
	case "order", "query":
		s.publicQuery(w, r, inputFromValues(r.Form))
	default:
		writeError(w, 400, 40002, "unsupported act", requestID(r))
	}
}
func (s *Service) publicQuery(w http.ResponseWriter, r *http.Request, input paymentInput) {
	input.normalize()
	if input.MerchantID == "" || input.MerchantOrderNo == "" || input.Sign == "" {
		writeError(w, 400, 40002, "missing query fields", requestID(r))
		return
	}
	account, err := s.accountByMerchantNo(r.Context(), input.MerchantID)
	if err != nil {
		writeError(w, 404, 40005, "merchant not found or disabled", requestID(r))
		return
	}
	if err := verifyOPSSignature(input, account.APISecret); err != nil {
		writeError(w, 401, 40001, "invalid signature", requestID(r))
		return
	}
	bill, err := s.billByMerchantOrder(r.Context(), account.ID, input.MerchantOrderNo)
	if err != nil {
		writeError(w, 404, 40401, "order not found", requestID(r))
		return
	}
	if bill.Status == "pending" && s.shouldPollBill(bill.ID) {
		if updated, changed, pollErr := s.pollBill(r.Context(), bill); pollErr != nil {
			s.logger.Warn("payment polling failed", "bill_id", bill.ID, "error", pollErr)
		} else if changed {
			bill = updated
		}
	}
	writeJSON(w, 200, map[string]any{"code": 0, "message": "success", "data": billPublic(bill)})
}

func (s *Service) shouldPollBill(id uuid.UUID) bool {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	if s.pollAt == nil {
		s.pollAt = map[uuid.UUID]time.Time{}
	}
	now := time.Now()
	if last, ok := s.pollAt[id]; ok && now.Sub(last) < 5*time.Second {
		return false
	}
	s.pollAt[id] = now
	return true
}

func (s *Service) pollBill(ctx context.Context, bill billRecord) (billRecord, bool, error) {
	channel, err := s.channelByID(ctx, bill.ChannelID)
	if err != nil {
		return bill, false, err
	}
	status, err := s.queryPaymentStatus(ctx, channel, bill)
	if err != nil {
		return bill, false, err
	}
	if !status.Paid {
		return bill, false, nil
	}
	raw, _ := json.Marshal(status.Raw)
	paidAt := nowUTC()
	result := s.db.DB().WithContext(ctx).Model(&Bill{}).Where("id = ? AND status = ?", bill.ID, "pending").Updates(map[string]any{"status": "paid", "provider_transaction_id": nullableString(status.TransactionID), "provider_payload": string(raw), "paid_at": paidAt})
	if result.Error != nil {
		return bill, false, result.Error
	}
	if result.RowsAffected == 0 {
		current, err := s.billByID(ctx, bill.ID)
		return current, false, err
	}
	bill.Status, bill.ProviderTransactionID, bill.PaidAt = "paid", status.TransactionID, &paidAt
	s.audit(ctx, &bill.AccountID, nil, "bill.poll_paid", "bill", bill.ID.String(), "", map[string]any{"provider": bill.Provider})
	go s.notifyMerchant(context.Background(), bill)
	return bill, true, nil
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	config, err := s.loadSiteConfig(r.Context())
	if err != nil {
		writeError(w, 500, 50001, "cannot load site configuration", requestID(r))
		return
	}
	if !config.AllowPasswordLogin {
		writeError(w, http.StatusForbidden, 40301, "password login is disabled", requestID(r))
		return
	}
	var input struct {
		Identifier string `json:"identifier"`
		Email      string `json:"email"`
		Password   string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	identifier := strings.ToLower(strings.TrimSpace(input.Identifier))
	if identifier == "" {
		identifier = strings.ToLower(strings.TrimSpace(input.Email))
	}
	var user User
	err = s.db.DB().WithContext(r.Context()).Where("email = ? OR username = ?", identifier, identifier).Take(&user).Error
	if err != nil || !user.IsActive || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)) != nil {
		writeError(w, 401, 40103, "invalid username or password", requestID(r))
		return
	}
	claims := jwtClaims{Role: user.Role, Email: user.Email, RegisteredClaims: jwt.RegisteredClaims{Subject: user.ID.String(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())}}
	if user.AccountID != nil {
		claims.AccountID = user.AccountID.String()
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.jwtSecret)
	if err != nil {
		writeError(w, 500, 50001, "cannot issue access token", requestID(r))
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": signed, "token_type": "Bearer", "expires_in": 28800, "user": map[string]any{"id": user.ID, "email": claims.Email, "username": user.Username, "display_name": user.DisplayName, "role": user.Role, "account_id": user.AccountID}})
}

// setupStatus is deliberately minimal: it only reveals whether an initial
// user needs to be created, never any account metadata.
func (s *Service) setupStatus(w http.ResponseWriter, r *http.Request) {
	var users int64
	if err := s.db.DB().WithContext(r.Context()).Model(&User{}).Count(&users).Error; err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot determine setup state", requestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"required": users == 0})
}

func (s *Service) getPublicSiteConfig(w http.ResponseWriter, r *http.Request) {
	config, err := s.loadSiteConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot load site configuration", requestID(r))
		return
	}
	oidcConfig, err := s.loadPlatformOIDC(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot load OIDC configuration", requestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site_name": config.SiteName, "allow_password_login": config.AllowPasswordLogin, "allow_password_registration": config.AllowPasswordRegistration, "email_whitelist": config.EmailWhitelist, "terms_url": config.TermsURL, "privacy_policy_url": config.PrivacyPolicyURL, "favicon_url": config.FaviconURL, "theme_color": config.ThemeColor, "oidc_enabled": oidcConfig.Enabled, "oidc_login_label": oidcConfig.LoginLabel})
}

func (s *Service) loadPlatformOIDC(ctx context.Context) (oidcSiteConfig, error) {
	var admin User
	err := s.db.DB().WithContext(ctx).Where("role = ? AND account_id IS NOT NULL", "platform_admin").Order("created_at ASC").Take(&admin).Error
	if isNoRows(err) {
		return oidcSiteConfig{}, nil
	}
	if err != nil {
		return oidcSiteConfig{}, err
	}
	settings, _, err := s.loadAccountSettings(ctx, *admin.AccountID)
	return settings.OIDC, err
}

func (s *Service) oidcLogin(w http.ResponseWriter, r *http.Request) {
	config, err := s.loadPlatformOIDC(r.Context())
	if err != nil || !config.Enabled || config.IssuerURL == "" || config.ClientID == "" || config.RedirectURL == "" {
		writeError(w, http.StatusNotFound, 40401, "OIDC login is not configured", requestID(r))
		return
	}
	provider, err := s.oidcProvider(r.Context(), config)
	if err != nil {
		writeError(w, http.StatusBadGateway, 50201, "cannot initialize OIDC provider", requestID(r))
		return
	}
	state := randomToken(32)
	http.SetCookie(w, &http.Cookie{Name: "tsumugi_oidc_state", Value: state, Path: "/api/v1/auth/oidc", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.environment == "production", MaxAge: 600})
	oauthConfig := oauth2.Config{ClientID: config.ClientID, ClientSecret: config.ClientSecret, Endpoint: provider.Endpoint(), RedirectURL: config.RedirectURL, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}
	http.Redirect(w, r, oauthConfig.AuthCodeURL(state), http.StatusFound)
}

func (s *Service) oidcCallback(w http.ResponseWriter, r *http.Request) {
	config, err := s.loadPlatformOIDC(r.Context())
	if err != nil || !config.Enabled || config.IssuerURL == "" || config.ClientID == "" || config.RedirectURL == "" {
		writeError(w, http.StatusNotFound, 40401, "OIDC login is not configured", requestID(r))
		return
	}
	cookie, cookieErr := r.Cookie("tsumugi_oidc_state")
	if cookieErr != nil || cookie.Value == "" || !hmac.Equal([]byte(cookie.Value), []byte(r.URL.Query().Get("state"))) {
		writeError(w, http.StatusBadRequest, 40002, "invalid OIDC state", requestID(r))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "tsumugi_oidc_state", Value: "", Path: "/api/v1/auth/oidc", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.environment == "production", MaxAge: -1})
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		writeError(w, http.StatusUnauthorized, 40103, "OIDC provider rejected login: "+providerError, requestID(r))
		return
	}
	provider, err := s.oidcProvider(r.Context(), config)
	if err != nil {
		writeError(w, http.StatusBadGateway, 50201, "cannot initialize OIDC provider", requestID(r))
		return
	}
	oauthConfig := oauth2.Config{ClientID: config.ClientID, ClientSecret: config.ClientSecret, Endpoint: provider.Endpoint(), RedirectURL: config.RedirectURL}
	token, err := oauthConfig.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		writeError(w, http.StatusUnauthorized, 40103, "OIDC code exchange failed", requestID(r))
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		writeError(w, http.StatusUnauthorized, 40103, "OIDC response did not include an ID token", requestID(r))
		return
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: config.ClientID}).Verify(r.Context(), rawIDToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, 40103, "invalid OIDC ID token", requestID(r))
		return
	}
	var claims struct {
		Subject           string `json:"sub"`
		Email             string `json:"email"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := idToken.Claims(&claims); err != nil || claims.Subject == "" || strings.TrimSpace(claims.Email) == "" {
		writeError(w, http.StatusUnauthorized, 40103, "OIDC account must provide a subject and email", requestID(r))
		return
	}
	claims.Email = strings.ToLower(strings.TrimSpace(claims.Email))
	identity := config.IssuerURL + "|" + claims.Subject
	var user User
	err = s.db.DB().WithContext(r.Context()).Where("oidc_subject = ?", identity).Take(&user).Error
	if isNoRows(err) {
		if existingErr := s.db.DB().WithContext(r.Context()).Where("email = ?", claims.Email).Take(&user).Error; existingErr == nil {
			err = s.db.DB().WithContext(r.Context()).Model(&User{}).Where("id = ?", user.ID).Update("oidc_subject", identity).Error
			if err == nil {
				subject := identity
				user.OIDCSubject = &subject
			}
		} else if isNoRows(existingErr) {
			site, configErr := s.loadSiteConfig(r.Context())
			if configErr != nil {
				writeError(w, 500, 50001, "cannot load site configuration", requestID(r))
				return
			}
			if !emailAllowed(claims.Email, site.EmailWhitelist) {
				writeError(w, http.StatusForbidden, 40301, "email is not permitted to register", requestID(r))
				return
			}
			user, err = s.createOIDCUser(r.Context(), identity, claims.Email, claims.Name, claims.PreferredUsername)
		} else {
			err = existingErr
		}
	}
	if err != nil || !user.IsActive {
		writeError(w, http.StatusUnauthorized, 40103, "OIDC account is unavailable", requestID(r))
		return
	}
	signed, err := s.issueAccessToken(user)
	if err != nil {
		writeError(w, 500, 50001, "cannot issue access token", requestID(r))
		return
	}
	http.Redirect(w, r, strings.TrimRight(s.baseURL, "/")+"/?oidc_token="+url.QueryEscape(signed), http.StatusFound)
}

func (s *Service) oidcProvider(ctx context.Context, config oidcSiteConfig) (*oidc.Provider, error) {
	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return oidc.NewProvider(oidc.ClientContext(ctx, client), config.IssuerURL)
}

func (s *Service) createOIDCUser(ctx context.Context, subject, email, name, preferredUsername string) (User, error) {
	username := normalizeUsername(preferredUsername)
	if !validUsername(username) {
		username = strings.Split(email, "@")[0]
	}
	accountID, userID := uuid.New(), uuid.New()
	apiSecret, callbackSecret := randomToken(32), randomToken(32)
	apiCipher, err := s.encrypt(apiSecret)
	if err != nil {
		return User{}, err
	}
	callbackCipher, err := s.encrypt(callbackSecret)
	if err != nil {
		return User{}, err
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(randomToken(32)), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = username
	}
	user := User{ID: userID, AccountID: &accountID, Email: email, Username: username, OIDCSubject: &subject, PasswordHash: string(passwordHash), DisplayName: strings.TrimSpace(name), Role: "user", IsActive: true}
	err = s.db.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		available, err := nextAvailableUsername(tx, username)
		if err != nil {
			return err
		}
		user.Username = available
		merchantNo, err := nextMerchantNo(tx)
		if err != nil {
			return err
		}
		if err := tx.Create(&Account{ID: accountID, Name: user.DisplayName, MerchantNo: merchantNo, APISecretCiphertext: apiCipher, CallbackSecretCiphertext: callbackCipher}).Error; err != nil {
			return err
		}
		return tx.Create(&user).Error
	})
	if err == nil {
		s.audit(ctx, &accountID, &userID, "user.oidc_register", "user", userID.String(), "", map[string]string{"email": email})
	}
	return user, err
}

func (s *Service) issueAccessToken(user User) (string, error) {
	claims := jwtClaims{Role: user.Role, Email: user.Email, RegisteredClaims: jwt.RegisteredClaims{Subject: user.ID.String(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())}}
	if user.AccountID != nil {
		claims.AccountID = user.AccountID.String()
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.jwtSecret)
}

func (s *Service) register(w http.ResponseWriter, r *http.Request) {
	config, err := s.loadSiteConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot load site configuration", requestID(r))
		return
	}
	if !config.AllowPasswordRegistration {
		writeError(w, http.StatusForbidden, 40301, "password registration is disabled", requestID(r))
		return
	}
	var input struct {
		DisplayName string `json:"display_name"`
		AccountName string `json:"account_name"`
		Email       string `json:"email"`
		Username    string `json:"username"`
		Password    string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.DisplayName, input.AccountName, input.Email, input.Username = strings.TrimSpace(input.DisplayName), strings.TrimSpace(input.AccountName), strings.ToLower(strings.TrimSpace(input.Email)), normalizeUsername(input.Username)
	if input.Username == "" {
		input.Username = usernameFromEmail(input.Email)
	}
	if input.DisplayName == "" || input.AccountName == "" || input.Email == "" || !validUsername(input.Username) || len(input.Password) < 10 {
		writeError(w, http.StatusBadRequest, 40002, "account_name, display_name, email, username and a 10-character password are required", requestID(r))
		return
	}
	if !emailAllowed(input.Email, config.EmailWhitelist) {
		writeError(w, http.StatusForbidden, 40301, "email is not permitted to register", requestID(r))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, 500, 50001, "cannot secure password", requestID(r))
		return
	}
	accountID, userID := uuid.New(), uuid.New()
	apiSecret, callbackSecret := randomToken(32), randomToken(32)
	apiCiphertext, err := s.encrypt(apiSecret)
	if err != nil {
		writeError(w, 500, 50001, "cannot secure account credentials", requestID(r))
		return
	}
	callbackCiphertext, err := s.encrypt(callbackSecret)
	if err != nil {
		writeError(w, 500, 50001, "cannot secure account credentials", requestID(r))
		return
	}
	var merchantNo string
	err = s.db.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var accounts []Account
		if err := tx.Select("merchant_no").Find(&accounts).Error; err != nil {
			return err
		}
		max := int64(999)
		for _, account := range accounts {
			if value, parseErr := strconv.ParseInt(account.MerchantNo, 10, 64); parseErr == nil && value > max {
				max = value
			}
		}
		merchantNo = strconv.FormatInt(max+1, 10)
		if err := tx.Create(&Account{ID: accountID, Name: input.AccountName, MerchantNo: merchantNo, APISecretCiphertext: apiCiphertext, CallbackSecretCiphertext: callbackCiphertext}).Error; err != nil {
			return err
		}
		return tx.Create(&User{ID: userID, AccountID: &accountID, Email: input.Email, Username: input.Username, PasswordHash: string(hash), DisplayName: input.DisplayName, Role: "user", IsActive: true}).Error
	})
	if err != nil {
		writeError(w, http.StatusConflict, 40006, "cannot create user account", requestID(r))
		return
	}
	s.audit(r.Context(), &accountID, &userID, "user.register", "user", userID.String(), requestID(r), map[string]string{"email": input.Email})
	writeJSON(w, http.StatusCreated, map[string]any{"user": map[string]any{"id": userID, "email": input.Email, "username": input.Username, "display_name": input.DisplayName}, "account": map[string]any{"id": accountID, "name": input.AccountName, "merchant_no": merchantNo}, "credentials": map[string]string{"api_secret": apiSecret, "callback_secret": callbackSecret}})
}

// setupInitialize creates the one and only first platform administrator. A
// database guard makes this endpoint single-use even if two browser tabs race.
func (s *Service) setupInitialize(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		AccountName string `json:"account_name"`
		MerchantNo  string `json:"merchant_no"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Username = normalizeUsername(input.Username)
	if input.Username == "" {
		input.Username = usernameFromEmail(input.Email)
	}
	input.AccountName = strings.TrimSpace(input.AccountName)
	input.MerchantNo = strings.TrimSpace(input.MerchantNo)
	if input.DisplayName == "" || input.Email == "" || input.AccountName == "" || !validUsername(input.Username) || len(input.Password) < 10 {
		writeError(w, http.StatusBadRequest, 40002, "account_name, display_name, email, username and a 10-character password are required", requestID(r))
		return
	}
	if input.MerchantNo == "" {
		input.MerchantNo = "1000"
	}
	merchantNo, parseErr := strconv.ParseInt(input.MerchantNo, 10, 64)
	if parseErr != nil || merchantNo < 1000 || input.MerchantNo != strconv.FormatInt(merchantNo, 10) {
		writeError(w, http.StatusBadRequest, 40002, "merchant_no must be a decimal number starting at 1000", requestID(r))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot secure password", requestID(r))
		return
	}
	adminID := uuid.New()
	accountID := uuid.New()
	apiSecret := randomToken(32)
	callbackSecret := randomToken(32)
	apiCiphertext, encryptErr := s.encrypt(apiSecret)
	callbackCiphertext, callbackEncryptErr := s.encrypt(callbackSecret)
	if encryptErr != nil || callbackEncryptErr != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot secure account credentials", requestID(r))
		return
	}
	err = s.db.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var existingUsers int64
		if err := tx.Model(&User{}).Count(&existingUsers).Error; err != nil {
			return err
		}
		if existingUsers != 0 {
			return errSetupComplete
		}
		if err := tx.Create(&SystemSetting{SettingKey: "oobe_complete", SettingValue: nowUTC().Format(time.RFC3339)}).Error; err != nil {
			return err
		}
		if err := tx.Create(&Account{ID: accountID, Name: input.AccountName, MerchantNo: input.MerchantNo, APISecretCiphertext: apiCiphertext, CallbackSecretCiphertext: callbackCiphertext}).Error; err != nil {
			return err
		}
		return tx.Create(&User{ID: adminID, AccountID: &accountID, Email: input.Email, Username: input.Username, PasswordHash: string(hash), DisplayName: input.DisplayName, Role: "platform_admin", IsActive: true}).Error
	})
	if err != nil {
		if errors.Is(err, errSetupComplete) || isUniqueViolation(err) {
			writeError(w, http.StatusConflict, 40901, "initial setup has already been completed", requestID(r))
		} else {
			writeError(w, http.StatusInternalServerError, 50001, "cannot complete setup", requestID(r))
		}
		return
	}
	s.audit(r.Context(), &accountID, &adminID, "system.oobe_complete", "account", accountID.String(), requestID(r), map[string]string{"email": input.Email, "account_name": input.AccountName})
	writeJSON(w, http.StatusCreated, map[string]any{"message": "initial user created", "user": map[string]any{"id": adminID, "email": input.Email, "username": input.Username, "display_name": input.DisplayName, "role": "platform_admin"}, "account": map[string]any{"id": accountID, "name": input.AccountName, "merchant_no": input.MerchantNo}, "credentials": map[string]string{"api_secret": apiSecret, "callback_secret": callbackSecret}})
}

func (s *Service) admin(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/admin")
	switch {
	case path == "/me":
		if r.Method == "GET" {
			s.adminMe(w, r)
		} else if r.Method == "PATCH" {
			s.patchCurrentUser(w, r)
		} else {
			methodNotAllowed(w, r)
		}
	case r.Method == "GET" && path == "/dashboard":
		s.dashboard(w, r)
	case r.Method == "GET" && path == "/developer-credentials":
		s.getDeveloperCredentials(w, r)
	case path == "/users":
		if r.Method == "GET" {
			s.listUsers(w, r)
		} else if r.Method == "POST" {
			s.createUser(w, r)
		} else {
			methodNotAllowed(w, r)
		}
	case strings.HasPrefix(path, "/users/") && r.Method == "PATCH":
		s.patchUser(w, r, strings.TrimPrefix(path, "/users/"))
	case path == "/channels":
		if r.Method == "GET" {
			s.listChannels(w, r)
		} else if r.Method == "POST" {
			s.createChannel(w, r)
		} else {
			methodNotAllowed(w, r)
		}
	case strings.HasPrefix(path, "/channels/") && r.Method == "PATCH":
		s.patchChannel(w, r, strings.TrimPrefix(path, "/channels/"))
	case strings.HasPrefix(path, "/channels/") && r.Method == "DELETE":
		s.deleteChannel(w, r, strings.TrimPrefix(path, "/channels/"))
	case path == "/bills" && r.Method == "GET":
		s.listBills(w, r)
	case strings.HasPrefix(path, "/bills/") && strings.HasSuffix(path, "/refunds") && r.Method == "POST":
		s.createRefund(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/bills/"), "/refunds"))
	case strings.HasPrefix(path, "/bills/") && strings.HasSuffix(path, "/close") && r.Method == "POST":
		s.closeBill(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/bills/"), "/close"))
	case strings.HasPrefix(path, "/bills/") && strings.HasSuffix(path, "/reconcile") && r.Method == "POST":
		s.reconcileBill(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/bills/"), "/reconcile"))
	case strings.HasPrefix(path, "/bills/") && strings.HasSuffix(path, "/notify") && r.Method == "POST":
		s.retryMerchantCallback(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/bills/"), "/notify"))
	case strings.HasPrefix(path, "/bills/") && r.Method == "GET":
		s.getBill(w, r, strings.TrimPrefix(path, "/bills/"))
	case path == "/refunds" && r.Method == "GET":
		s.listRefunds(w, r)
	case path == "/audit-logs" && r.Method == "GET":
		s.listAuditLogs(w, r)
	case path == "/site-settings/oidc-discovery" && r.Method == "POST":
		s.discoverOIDC(w, r)
	case path == "/site-settings":
		if r.Method == "GET" {
			s.getSiteSettings(w, r)
		} else if r.Method == "PATCH" {
			s.patchSiteSettings(w, r)
		} else {
			methodNotAllowed(w, r)
		}
	default:
		writeError(w, 404, 40401, "admin endpoint not found", requestID(r))
	}
}
func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeError(w, 405, 40002, "method not allowed", requestID(r))
}
func (s *Service) adminMe(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	var user User
	if err := s.db.DB().WithContext(r.Context()).Select("username", "display_name").Take(&user, "id = ?", p.UserID).Error; err != nil {
		writeError(w, 500, 50001, "cannot load user profile", requestID(r))
		return
	}
	response := map[string]any{"id": p.UserID, "email": p.Email, "username": user.Username, "display_name": user.DisplayName, "role": p.Role, "account_id": p.AccountID}
	if p.AccountID != nil {
		var account Account
		if err := s.db.DB().WithContext(r.Context()).Select("name", "merchant_no", "status").Take(&account, "id = ?", *p.AccountID).Error; err == nil {
			response["account"] = map[string]any{"id": p.AccountID, "name": account.Name, "merchant_no": account.MerchantNo, "status": account.Status}
		}
	}
	writeJSON(w, 200, response)
}

func (s *Service) getSiteSettings(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.siteSettingsAccount(w, r)
	if !ok {
		return
	}
	settings, _, err := s.loadAccountSettings(r.Context(), *accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot load site settings", requestID(r))
		return
	}
	config, err := s.loadSiteConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot load site configuration", requestID(r))
		return
	}
	response := siteSettingsPublic(settings)
	response["site"] = config
	writeJSON(w, http.StatusOK, response)
}

func (s *Service) patchSiteSettings(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.siteSettingsAccount(w, r)
	if !ok {
		return
	}
	var input struct {
		Site     *siteConfig         `json:"site"`
		Email    *smtpSiteConfig     `json:"email"`
		HCaptcha *hcaptchaSiteConfig `json:"hcaptcha"`
		OIDC     *oidcSiteConfig     `json:"oidc"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Site != nil {
		if err := s.saveSiteConfig(r.Context(), *input.Site); err != nil {
			writeError(w, http.StatusInternalServerError, 50001, "cannot save site configuration", requestID(r))
			return
		}
	}
	settings, exists, err := s.loadAccountSettings(r.Context(), *accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot load site settings", requestID(r))
		return
	}
	if input.Email != nil {
		if input.Email.Password == "" {
			input.Email.Password = settings.Email.Password
		}
		settings.Email = *input.Email
	}
	if input.HCaptcha != nil {
		if input.HCaptcha.SecretKey == "" {
			input.HCaptcha.SecretKey = settings.HCaptcha.SecretKey
		}
		settings.HCaptcha = *input.HCaptcha
	}
	if input.OIDC != nil {
		if input.OIDC.ClientSecret == "" {
			input.OIDC.ClientSecret = settings.OIDC.ClientSecret
		}
		settings.OIDC = *input.OIDC
	}
	emailCiphertext, err := s.encryptConfig(settings.Email)
	if err == nil {
		var hcaptchaCiphertext, oidcCiphertext string
		hcaptchaCiphertext, err = s.encryptConfig(settings.HCaptcha)
		if err == nil {
			oidcCiphertext, err = s.encryptConfig(settings.OIDC)
		}
		if err == nil {
			settingsModel := AccountSetting{AccountID: *accountID, EmailConfigCiphertext: emailCiphertext, HCaptchaConfigCiphertext: hcaptchaCiphertext, OIDCConfigCiphertext: oidcCiphertext}
			if exists {
				err = s.db.DB().WithContext(r.Context()).Save(&settingsModel).Error
			} else {
				err = s.db.DB().WithContext(r.Context()).Create(&settingsModel).Error
			}
		}
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot save site settings", requestID(r))
		return
	}
	p := currentPrincipal(r)
	s.audit(r.Context(), accountID, &p.UserID, "site_settings.update", "site_settings", accountID.String(), requestID(r), map[string]bool{"site": input.Site != nil, "email": input.Email != nil, "hcaptcha": input.HCaptcha != nil, "oidc": input.OIDC != nil})
	config, _ := s.loadSiteConfig(r.Context())
	response := siteSettingsPublic(settings)
	response["site"] = config
	writeJSON(w, http.StatusOK, response)
}

func (s *Service) discoverOIDC(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.siteSettingsAccount(w, r); !ok {
		return
	}
	var input struct {
		IssuerURL string `json:"issuer_url"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	issuer, err := s.normalizedOIDCIssuer(input.IssuerURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, 40002, "invalid OIDC issuer URL", requestID(r))
		return
	}
	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, 40002, "invalid OIDC issuer URL", requestID(r))
		return
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, 50201, "cannot discover OIDC provider", requestID(r))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		writeError(w, http.StatusBadGateway, 50201, "OIDC provider discovery failed", requestID(r))
		return
	}
	var discovery struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		JWKSURI               string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&discovery); err != nil {
		writeError(w, http.StatusBadGateway, 50201, "invalid OIDC discovery response", requestID(r))
		return
	}
	discoveredIssuer, err := s.normalizedOIDCIssuer(discovery.Issuer)
	if err != nil || discoveredIssuer != issuer || !s.validExternalURL(discovery.AuthorizationEndpoint) || !s.validExternalURL(discovery.TokenEndpoint) || !s.validExternalURL(discovery.JWKSURI) {
		writeError(w, http.StatusBadGateway, 50201, "invalid OIDC discovery response", requestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"issuer_url": issuer, "authorization_endpoint": discovery.AuthorizationEndpoint,
		"token_endpoint": discovery.TokenEndpoint, "jwks_uri": discovery.JWKSURI,
	})
}

func (s *Service) siteSettingsAccount(w http.ResponseWriter, r *http.Request) (*uuid.UUID, bool) {
	p := currentPrincipal(r)
	if p.Role != "platform_admin" {
		writeError(w, http.StatusForbidden, 40301, "platform administrator required", requestID(r))
		return nil, false
	}
	accountID, ok := s.scopedAccount(w, r)
	if !ok || accountID == nil {
		if ok {
			writeError(w, http.StatusBadRequest, 40002, "account context is required", requestID(r))
		}
		return nil, false
	}
	return accountID, true
}

func (s *Service) loadAccountSettings(ctx context.Context, accountID uuid.UUID) (accountSiteSettings, bool, error) {
	var setting AccountSetting
	err := s.db.DB().WithContext(ctx).Take(&setting, "account_id = ?", accountID).Error
	if isNoRows(err) {
		return accountSiteSettings{}, false, nil
	}
	if err != nil {
		return accountSiteSettings{}, false, err
	}
	settings := accountSiteSettings{}
	if err = s.decryptConfig(setting.EmailConfigCiphertext, &settings.Email); err != nil {
		return settings, true, err
	}
	if err = s.decryptConfig(setting.HCaptchaConfigCiphertext, &settings.HCaptcha); err != nil {
		return settings, true, err
	}
	if err = s.decryptConfig(setting.OIDCConfigCiphertext, &settings.OIDC); err != nil {
		return settings, true, err
	}
	return settings, true, nil
}
func defaultSiteConfig() siteConfig {
	return siteConfig{SiteName: "Tsumugi Pay", ThemeColor: "#2f9c84", AllowPasswordLogin: true, AllowPasswordRegistration: false, EmailWhitelist: []string{}}
}
func (s *Service) loadSiteConfig(ctx context.Context) (siteConfig, error) {
	config := defaultSiteConfig()
	var setting SystemSetting
	err := s.db.DB().WithContext(ctx).Take(&setting, "setting_key = ?", "site_config").Error
	if isNoRows(err) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal([]byte(setting.SettingValue), &config); err != nil {
		return config, err
	}
	if config.SiteName == "" {
		config.SiteName = defaultSiteConfig().SiteName
	}
	if !validThemeColor(config.ThemeColor) {
		config.ThemeColor = defaultSiteConfig().ThemeColor
	}
	return config, nil
}
func (s *Service) saveSiteConfig(ctx context.Context, config siteConfig) error {
	config.SiteName = strings.TrimSpace(config.SiteName)
	if config.SiteName == "" {
		config.SiteName = defaultSiteConfig().SiteName
	}
	if len(config.SiteName) > 120 {
		return errors.New("site name is too long")
	}
	if config.ThemeColor == "" {
		config.ThemeColor = defaultSiteConfig().ThemeColor
	}
	if !validThemeColor(config.ThemeColor) {
		return errors.New("theme color must be a hex color")
	}
	config.FaviconURL = strings.TrimSpace(config.FaviconURL)
	if config.FaviconURL != "" && !strings.HasPrefix(config.FaviconURL, "/") && !s.validExternalURL(config.FaviconURL) {
		return errors.New("favicon URL must be an HTTPS URL or site path")
	}
	whitelist, err := normalizeEmailSuffixWhitelist(config.EmailWhitelist)
	if err != nil {
		return err
	}
	config.EmailWhitelist = whitelist
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return s.db.DB().WithContext(ctx).Save(&SystemSetting{SettingKey: "site_config", SettingValue: string(encoded)}).Error
}
func normalizeEmailSuffixWhitelist(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if !strings.HasPrefix(value, "@") {
			value = "@" + value
		}
		if len(value) < 4 || strings.Count(value, "@") != 1 || strings.ContainsAny(value, " /\\") {
			return nil, errors.New("email whitelist entries must be email suffixes such as @example.com")
		}
		result = append(result, value)
	}
	return result, nil
}
func emailAllowed(email string, whitelist []string) bool {
	if len(whitelist) == 0 {
		return true
	}
	for _, entry := range whitelist {
		if strings.HasPrefix(entry, "@") && strings.HasSuffix(email, entry) {
			return true
		}
	}
	return false
}
func validThemeColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, char := range value[1:] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}
func normalizeUsername(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func usernameFromEmail(email string) string { return normalizeUsername(strings.Split(email, "@")[0]) }
func validUsername(value string) bool {
	if len(value) < 3 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-') {
			return false
		}
	}
	return true
}
func nextMerchantNo(tx *gorm.DB) (string, error) {
	var accounts []Account
	if err := tx.Select("merchant_no").Find(&accounts).Error; err != nil {
		return "", err
	}
	maximum := int64(999)
	for _, account := range accounts {
		if value, parseErr := strconv.ParseInt(account.MerchantNo, 10, 64); parseErr == nil && value > maximum {
			maximum = value
		}
	}
	return strconv.FormatInt(maximum+1, 10), nil
}
func nextAvailableUsername(tx *gorm.DB, preferred string) (string, error) {
	base := normalizeUsername(preferred)
	if !validUsername(base) {
		base = "user"
	}
	for suffix := 1; ; suffix++ {
		candidate := base
		if suffix > 1 {
			candidate = fmt.Sprintf("%s-%d", base, suffix)
		}
		var count int64
		if err := tx.Model(&User{}).Where("username = ?", candidate).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
}

func (s *Service) encryptConfig(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return s.encrypt(string(encoded))
}
func (s *Service) decryptConfig(ciphertext string, target any) error {
	if ciphertext == "" {
		return nil
	}
	plain, err := s.decrypt(ciphertext)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(plain), target)
}
func siteSettingsPublic(settings accountSiteSettings) map[string]any {
	return map[string]any{
		"email":    map[string]any{"host": settings.Email.Host, "port": settings.Email.Port, "username": settings.Email.Username, "from": settings.Email.From, "password_configured": settings.Email.Password != ""},
		"hcaptcha": map[string]any{"enabled": settings.HCaptcha.Enabled, "site_key": settings.HCaptcha.SiteKey, "secret_key_configured": settings.HCaptcha.SecretKey != ""},
		"oidc":     map[string]any{"enabled": settings.OIDC.Enabled, "issuer_url": settings.OIDC.IssuerURL, "client_id": settings.OIDC.ClientID, "redirect_url": settings.OIDC.RedirectURL, "authorization_endpoint": settings.OIDC.AuthorizationEndpoint, "token_endpoint": settings.OIDC.TokenEndpoint, "jwks_uri": settings.OIDC.JWKSURI, "login_label": settings.OIDC.LoginLabel, "client_secret_configured": settings.OIDC.ClientSecret != ""},
	}
}

func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if p.Role != "platform_admin" {
		writeError(w, 403, 40301, "platform administrator required", requestID(r))
		return
	}
	var users []User
	if err := s.db.DB().WithContext(r.Context()).Preload("Account").Order("created_at DESC").Find(&users).Error; err != nil {
		writeError(w, 500, 50001, "cannot load users", requestID(r))
		return
	}
	items := make([]map[string]any, 0, len(users))
	for _, user := range users {
		item := map[string]any{"id": user.ID, "email": user.Email, "username": user.Username, "display_name": user.DisplayName, "role": user.Role, "is_active": user.IsActive, "created_at": user.CreatedAt}
		if user.Account != nil {
			item["account"] = map[string]any{"name": user.Account.Name, "merchant_no": user.Account.MerchantNo}
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Service) createUser(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if p.Role != "platform_admin" {
		writeError(w, 403, 40301, "platform administrator required", requestID(r))
		return
	}
	var input struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		AccountName string `json:"account_name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.DisplayName, input.Email, input.AccountName, input.Username = strings.TrimSpace(input.DisplayName), strings.ToLower(strings.TrimSpace(input.Email)), strings.TrimSpace(input.AccountName), normalizeUsername(input.Username)
	if input.Username == "" {
		input.Username = usernameFromEmail(input.Email)
	}
	if input.DisplayName == "" || input.Email == "" || input.AccountName == "" || !validUsername(input.Username) || len(input.Password) < 10 {
		writeError(w, 400, 40002, "account_name, display_name, email, username and a 10-character password are required", requestID(r))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, 500, 50001, "cannot secure password", requestID(r))
		return
	}
	accountID, userID := uuid.New(), uuid.New()
	api, callback := randomToken(32), randomToken(32)
	apiCipher, err := s.encrypt(api)
	if err != nil {
		writeError(w, 500, 50001, "cannot secure account credentials", requestID(r))
		return
	}
	callbackCipher, err := s.encrypt(callback)
	if err != nil {
		writeError(w, 500, 50001, "cannot secure account credentials", requestID(r))
		return
	}
	var merchantNo string
	err = s.db.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var accounts []Account
		if err := tx.Select("merchant_no").Find(&accounts).Error; err != nil {
			return err
		}
		maximum := int64(999)
		for _, account := range accounts {
			if value, parseErr := strconv.ParseInt(account.MerchantNo, 10, 64); parseErr == nil && value > maximum {
				maximum = value
			}
		}
		merchantNo = strconv.FormatInt(maximum+1, 10)
		if err := tx.Create(&Account{ID: accountID, Name: input.AccountName, MerchantNo: merchantNo, APISecretCiphertext: apiCipher, CallbackSecretCiphertext: callbackCipher}).Error; err != nil {
			return err
		}
		return tx.Create(&User{ID: userID, AccountID: &accountID, Email: input.Email, Username: input.Username, PasswordHash: string(hash), DisplayName: input.DisplayName, Role: "user", IsActive: true}).Error
	})
	if err != nil {
		writeError(w, 409, 40006, "cannot create user", requestID(r))
		return
	}
	s.audit(r.Context(), &accountID, &p.UserID, "user.create", "user", userID.String(), requestID(r), map[string]string{"email": input.Email})
	writeJSON(w, 201, map[string]any{"id": userID, "email": input.Email, "username": input.Username, "merchant_no": merchantNo, "credentials": map[string]string{"api_secret": api, "callback_secret": callback}})
}
func (s *Service) patchUser(w http.ResponseWriter, r *http.Request, idText string) {
	p := currentPrincipal(r)
	if p.Role != "platform_admin" {
		writeError(w, 403, 40301, "platform administrator required", requestID(r))
		return
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		writeError(w, 400, 40002, "invalid user id", requestID(r))
		return
	}
	var input struct {
		IsActive    *bool   `json:"is_active"`
		DisplayName *string `json:"display_name"`
		Username    *string `json:"username"`
		Password    *string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	updates := map[string]any{}
	if input.IsActive != nil {
		updates["is_active"] = *input.IsActive
	}
	if input.DisplayName != nil {
		updates["display_name"] = strings.TrimSpace(*input.DisplayName)
	}
	if input.Username != nil {
		username := normalizeUsername(*input.Username)
		if !validUsername(username) {
			writeError(w, 400, 40002, "username must be 3-64 letters, numbers, _ or -", requestID(r))
			return
		}
		updates["username"] = username
	}
	if input.Password != nil {
		if len(*input.Password) < 10 {
			writeError(w, 400, 40002, "password must be at least 10 characters", requestID(r))
			return
		}
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(*input.Password), bcrypt.DefaultCost)
		if hashErr != nil {
			writeError(w, 500, 50001, "cannot secure password", requestID(r))
			return
		}
		updates["password_hash"] = string(hash)
	}
	if len(updates) > 0 {
		if err := s.db.DB().WithContext(r.Context()).Model(&User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			writeError(w, 500, 50001, "cannot update user", requestID(r))
			return
		}
	}
	s.audit(r.Context(), nil, &p.UserID, "user.update", "user", id.String(), requestID(r), updates)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) patchCurrentUser(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	var input struct {
		Username    *string `json:"username"`
		DisplayName *string `json:"display_name"`
		Password    *string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	updates := map[string]any{}
	if input.Username != nil {
		username := normalizeUsername(*input.Username)
		if !validUsername(username) {
			writeError(w, 400, 40002, "username must be 3-64 letters, numbers, _ or -", requestID(r))
			return
		}
		updates["username"] = username
	}
	if input.DisplayName != nil {
		displayName := strings.TrimSpace(*input.DisplayName)
		if displayName == "" {
			writeError(w, 400, 40002, "display name is required", requestID(r))
			return
		}
		updates["display_name"] = displayName
	}
	if input.Password != nil {
		if len(*input.Password) < 10 {
			writeError(w, 400, 40002, "password must be at least 10 characters", requestID(r))
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*input.Password), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, 500, 50001, "cannot secure password", requestID(r))
			return
		}
		updates["password_hash"] = string(hash)
	}
	if len(updates) == 0 {
		writeError(w, 400, 40002, "no profile changes provided", requestID(r))
		return
	}
	if err := s.db.DB().WithContext(r.Context()).Model(&User{}).Where("id = ?", p.UserID).Updates(updates).Error; err != nil {
		writeError(w, 409, 40006, "cannot update profile", requestID(r))
		return
	}
	s.audit(r.Context(), p.AccountID, &p.UserID, "user.profile_update", "user", p.UserID.String(), requestID(r), map[string]bool{"username": input.Username != nil, "display_name": input.DisplayName != nil, "password": input.Password != nil})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) dashboard(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.scopedAccount(w, r)
	if !ok {
		return
	}
	var stats struct{ Total, Pending, Paid, Refunded, Volume int64 }
	query := s.db.DB().WithContext(r.Context()).Model(&Bill{}).Select("COUNT(*) AS total, COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0) AS pending, COALESCE(SUM(CASE WHEN status = 'paid' THEN 1 ELSE 0 END), 0) AS paid, COALESCE(SUM(CASE WHEN status = 'refunded' THEN 1 ELSE 0 END), 0) AS refunded, COALESCE(SUM(CASE WHEN status = 'paid' THEN amount_minor ELSE 0 END), 0) AS volume")
	if accountID != nil {
		query = query.Where("account_id = ?", *accountID)
	}
	if err := query.Scan(&stats).Error; err != nil {
		writeError(w, 500, 50001, "cannot load dashboard", requestID(r))
		return
	}
	writeJSON(w, 200, map[string]any{"total_bills": stats.Total, "pending_bills": stats.Pending, "paid_bills": stats.Paid, "refunded_bills": stats.Refunded, "paid_volume": moneyString(stats.Volume)})
}

func (s *Service) getDeveloperCredentials(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if !canManagePayments(p) {
		writeError(w, http.StatusForbidden, 40301, "payment operator role required", requestID(r))
		return
	}
	accountID, ok := s.scopedAccount(w, r)
	if !ok || accountID == nil {
		return
	}
	var account Account
	if err := s.db.DB().WithContext(r.Context()).Select("merchant_no", "api_secret_ciphertext").Take(&account, "id = ?", *accountID).Error; err != nil {
		writeError(w, http.StatusNotFound, 40401, "account not found", requestID(r))
		return
	}
	secret, err := s.decrypt(account.APISecretCiphertext)
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot read merchant key", requestID(r))
		return
	}
	s.audit(r.Context(), accountID, &p.UserID, "account.api_secret.view", "account", accountID.String(), requestID(r), nil)
	writeJSON(w, http.StatusOK, map[string]string{"merchant_no": account.MerchantNo, "api_secret": secret})
}

func (s *Service) scopedAccount(w http.ResponseWriter, r *http.Request) (*uuid.UUID, bool) {
	p := currentPrincipal(r)
	if p.AccountID == nil {
		writeError(w, 403, 40301, "a user account is required", requestID(r))
		return nil, false
	}
	return p.AccountID, true
}

func (s *Service) listAccounts(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if p.Role != "platform_admin" {
		writeError(w, 403, 40301, "platform administrator required", requestID(r))
		return
	}
	var accounts []Account
	if err := s.db.DB().WithContext(r.Context()).Order("created_at DESC").Find(&accounts).Error; err != nil {
		writeError(w, 500, 50001, "cannot load accounts", requestID(r))
		return
	}
	items := make([]map[string]any, 0)
	for _, account := range accounts {
		items = append(items, map[string]any{"id": account.ID, "name": account.Name, "merchant_no": account.MerchantNo, "status": account.Status, "created_at": account.CreatedAt})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Service) createAccount(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if p.Role != "platform_admin" {
		writeError(w, 403, 40301, "platform administrator required", requestID(r))
		return
	}
	var input struct {
		Name           string `json:"name"`
		MerchantNo     string `json:"merchant_no"`
		APISecret      string `json:"api_secret"`
		CallbackSecret string `json:"callback_secret"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Name == "" || input.MerchantNo == "" || input.APISecret == "" || input.CallbackSecret == "" {
		writeError(w, 400, 40002, "name, merchant_no, api_secret and callback_secret are required", requestID(r))
		return
	}
	api, err := s.encrypt(input.APISecret)
	if err != nil {
		writeError(w, 500, 50001, "cannot secure secret", requestID(r))
		return
	}
	callback, err := s.encrypt(input.CallbackSecret)
	if err != nil {
		writeError(w, 500, 50001, "cannot secure secret", requestID(r))
		return
	}
	id := uuid.New()
	err = s.db.DB().WithContext(r.Context()).Create(&Account{ID: id, Name: input.Name, MerchantNo: input.MerchantNo, APISecretCiphertext: api, CallbackSecretCiphertext: callback}).Error
	if err != nil {
		writeError(w, 409, 40006, "merchant number already exists", requestID(r))
		return
	}
	s.audit(r.Context(), &id, &p.UserID, "account.create", "account", id.String(), requestID(r), map[string]string{"name": input.Name})
	writeJSON(w, 201, map[string]any{"id": id, "name": input.Name, "merchant_no": input.MerchantNo})
}
func (s *Service) patchAccount(w http.ResponseWriter, r *http.Request, idText string) {
	p := currentPrincipal(r)
	if p.Role != "platform_admin" {
		writeError(w, 403, 40301, "platform administrator required", requestID(r))
		return
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		writeError(w, 400, 40002, "invalid account id", requestID(r))
		return
	}
	var input struct {
		Name           *string `json:"name"`
		Status         *string `json:"status"`
		APISecret      *string `json:"api_secret"`
		CallbackSecret *string `json:"callback_secret"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Status != nil && *input.Status != "active" && *input.Status != "suspended" {
		writeError(w, 400, 40002, "invalid account status", requestID(r))
		return
	}
	updates := map[string]any{}
	if input.Name != nil {
		updates["name"] = *input.Name
	}
	if input.Status != nil {
		updates["status"] = *input.Status
	}
	if input.APISecret != nil {
		value, encryptErr := s.encrypt(*input.APISecret)
		if encryptErr != nil {
			writeError(w, 500, 50001, "cannot secure secret", requestID(r))
			return
		}
		updates["api_secret_ciphertext"] = value
	}
	if input.CallbackSecret != nil {
		value, encryptErr := s.encrypt(*input.CallbackSecret)
		if encryptErr != nil {
			writeError(w, 500, 50001, "cannot secure secret", requestID(r))
			return
		}
		updates["callback_secret_ciphertext"] = value
	}
	if len(updates) > 0 {
		if err := s.db.DB().WithContext(r.Context()).Model(&Account{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			writeError(w, 500, 50001, "cannot update account", requestID(r))
			return
		}
	}
	s.audit(r.Context(), &id, &p.UserID, "account.update", "account", id.String(), requestID(r), map[string]any{"secret_rotated": input.APISecret != nil || input.CallbackSecret != nil})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) listChannels(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.scopedAccount(w, r)
	if !ok {
		return
	}
	if accountID == nil {
		writeError(w, 400, 40002, "a user account is required", requestID(r))
		return
	}
	var channels []PaymentChannel
	if err := s.db.DB().WithContext(r.Context()).Where("account_id = ?", *accountID).Order("provider").Find(&channels).Error; err != nil {
		writeError(w, 500, 50001, "cannot load channels", requestID(r))
		return
	}
	items := make([]map[string]any, 0)
	for _, channel := range channels {
		items = append(items, map[string]any{"id": channel.ID, "provider": channel.Provider, "display_name": channel.DisplayName, "priority": channel.Priority, "weight": channel.Weight, "enabled": channel.Enabled, "configured": channel.ConfigCiphertext != "", "config": s.publicChannelConfig(channel.ConfigCiphertext), "webhook_url": fmt.Sprintf("%s/api/v1/webhooks/%s/%s", s.baseURL, channel.Provider, channel.WebhookToken), "updated_at": channel.UpdatedAt})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

// publicChannelConfig provides the values that are useful to edit but are not
// credentials. Private keys and APIv3 keys must never be sent back to a browser.
func (s *Service) publicChannelConfig(ciphertext string) map[string]any {
	if ciphertext == "" {
		return nil
	}
	plain, err := s.decrypt(ciphertext)
	if err != nil {
		return nil
	}
	var config providerConfig
	if json.Unmarshal([]byte(plain), &config) != nil {
		return nil
	}
	if config.Alipay != nil {
		return map[string]any{"alipay": map[string]any{
			"pid": config.Alipay.PID, "mode": alipayPaymentMode(config.Alipay), "app_id": config.Alipay.AppID, "alipay_public_key_pem": config.Alipay.AlipayPublicKeyPEM,
			"gateway_url": config.Alipay.GatewayURL, "return_url": config.Alipay.ReturnURL,
			"app_private_key_configured": config.Alipay.AppPrivateKeyPEM != "",
		}}
	}
	if config.Wechat != nil {
		return map[string]any{"wechat": map[string]any{
			"mch_id": config.Wechat.MchID, "app_id": config.Wechat.AppID, "merchant_serial_no": config.Wechat.MerchantSerialNo,
			"platform_public_key_pem": config.Wechat.PlatformPublicKeyPEM, "platform_serial_no": config.Wechat.PlatformSerialNo,
			"merchant_private_key_configured": config.Wechat.MerchantPrivateKeyPEM != "", "api_v3_key_configured": config.Wechat.APIv3Key != "",
		}}
	}
	return nil
}
func (s *Service) createChannel(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if !canManagePayments(p) {
		writeError(w, http.StatusForbidden, 40301, "payment operator role required", requestID(r))
		return
	}
	accountID, ok := s.scopedAccount(w, r)
	if !ok || accountID == nil {
		if ok {
			writeError(w, http.StatusBadRequest, 40002, "a user account is required", requestID(r))
		}
		return
	}
	var input struct {
		Provider    string `json:"provider"`
		DisplayName string `json:"display_name"`
		Priority    int    `json:"priority"`
		Weight      int    `json:"weight"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Provider = strings.ToLower(strings.TrimSpace(input.Provider))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.Provider != "alipay" && input.Provider != "wechat" {
		writeError(w, http.StatusBadRequest, 40003, "provider must be alipay or wechat", requestID(r))
		return
	}
	if input.DisplayName == "" {
		input.DisplayName = map[string]string{"alipay": "支付宝", "wechat": "微信支付"}[input.Provider]
	}
	if input.Priority == 0 {
		input.Priority = 100
	}
	if input.Priority < 0 || input.Weight < 0 {
		writeError(w, http.StatusBadRequest, 40002, "priority and weight must not be negative", requestID(r))
		return
	}
	if input.Weight == 0 {
		input.Weight = 100
	}
	channelID := uuid.New()
	err := s.db.DB().WithContext(r.Context()).Create(&PaymentChannel{ID: channelID, AccountID: *accountID, Provider: input.Provider, DisplayName: input.DisplayName, Priority: input.Priority, Weight: input.Weight, WebhookToken: randomToken(24)}).Error
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot create payment channel", requestID(r))
		return
	}
	s.audit(r.Context(), accountID, &p.UserID, "channel.create", "payment_channel", channelID.String(), requestID(r), map[string]string{"provider": input.Provider})
	writeJSON(w, http.StatusCreated, map[string]any{"id": channelID, "provider": input.Provider, "display_name": input.DisplayName, "priority": input.Priority, "weight": input.Weight})
}
func (s *Service) patchChannel(w http.ResponseWriter, r *http.Request, idText string) {
	p := currentPrincipal(r)
	if !canManagePayments(p) {
		writeError(w, 403, 40301, "payment operator role required", requestID(r))
		return
	}
	channelID, err := uuid.Parse(idText)
	if err != nil {
		writeError(w, 400, 40002, "invalid channel id", requestID(r))
		return
	}
	var input struct {
		DisplayName *string         `json:"display_name"`
		Priority    *int            `json:"priority"`
		Weight      *int            `json:"weight"`
		Enabled     *bool           `json:"enabled"`
		Config      json.RawMessage `json:"config"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var channel PaymentChannel
	err = s.db.DB().WithContext(r.Context()).Take(&channel, "id = ?", channelID).Error
	if err != nil {
		writeError(w, 404, 40401, "channel not found", requestID(r))
		return
	}
	if !s.mayAccessAccount(p, channel.AccountID) {
		writeError(w, 403, 40301, "account access denied", requestID(r))
		return
	}
	updates := map[string]any{}
	if input.DisplayName != nil {
		updates["display_name"] = *input.DisplayName
	}
	if input.Priority != nil {
		if *input.Priority < 0 {
			writeError(w, 400, 40002, "priority must not be negative", requestID(r))
			return
		}
		updates["priority"] = *input.Priority
	}
	if input.Weight != nil {
		if *input.Weight < 1 {
			writeError(w, 400, 40002, "weight must be at least 1", requestID(r))
			return
		}
		updates["weight"] = *input.Weight
	}
	if input.Config != nil {
		var config providerConfig
		if err = json.Unmarshal(input.Config, &config); err != nil {
			writeError(w, 400, 40002, "invalid provider config", requestID(r))
			return
		}
		if channel.ConfigCiphertext != "" {
			plain, decryptErr := s.decrypt(channel.ConfigCiphertext)
			if decryptErr != nil {
				writeError(w, 500, 50001, "cannot read stored channel config", requestID(r))
				return
			}
			var stored providerConfig
			if err = json.Unmarshal([]byte(plain), &stored); err != nil {
				writeError(w, 500, 50001, "stored channel config is invalid", requestID(r))
				return
			}
			config = mergeProviderConfig(channel.Provider, stored, config)
		}
		if err = validateProviderConfig(channel.Provider, config); err != nil {
			writeError(w, 400, 40002, err.Error(), requestID(r))
			return
		}
		if channel.Provider == "wechat" && config.Wechat.PlatformPublicKeyPEM == "" {
			if err = s.fetchWechatPlatformCertificate(r.Context(), config.Wechat); err != nil {
				writeError(w, http.StatusBadGateway, 50201, "无法自动获取微信支付平台证书。若商户已启用微信支付公钥，请在商户平台的账户中心 - API安全申请并下载公钥，填写公钥 PEM 与公钥 ID："+err.Error(), requestID(r))
				return
			}
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			writeError(w, 500, 50001, "cannot encode channel config", requestID(r))
			return
		}
		encrypted, err := s.encrypt(string(encoded))
		if err != nil {
			writeError(w, 500, 50001, "cannot secure channel config", requestID(r))
			return
		}
		updates["config_ciphertext"] = encrypted
		channel.ConfigCiphertext = encrypted
	}
	if input.Enabled != nil {
		if *input.Enabled {
			if channel.ConfigCiphertext == "" {
				writeError(w, 400, 40002, "configure channel credentials before enabling", requestID(r))
				return
			}
		}
		updates["enabled"] = *input.Enabled
	}
	if len(updates) > 0 {
		if err := s.db.DB().WithContext(r.Context()).Model(&PaymentChannel{}).Where("id = ?", channelID).Updates(updates).Error; err != nil {
			writeError(w, 500, 50001, "cannot update payment channel", requestID(r))
			return
		}
	}
	s.audit(r.Context(), &channel.AccountID, &p.UserID, "channel.update", "payment_channel", channelID.String(), requestID(r), map[string]any{"provider": channel.Provider, "config_updated": input.Config != nil, "enabled": input.Enabled})
	w.WriteHeader(http.StatusNoContent)
}

func mergeProviderConfig(provider string, stored, input providerConfig) providerConfig {
	if provider == "alipay" && input.Alipay != nil {
		if stored.Alipay == nil {
			stored.Alipay = &alipayConfig{}
		}
		mergeString(&stored.Alipay.PID, input.Alipay.PID)
		mergeString(&stored.Alipay.Mode, input.Alipay.Mode)
		mergeString(&stored.Alipay.AppID, input.Alipay.AppID)
		mergeString(&stored.Alipay.AppPrivateKeyPEM, input.Alipay.AppPrivateKeyPEM)
		mergeString(&stored.Alipay.AlipayPublicKeyPEM, input.Alipay.AlipayPublicKeyPEM)
		mergeString(&stored.Alipay.GatewayURL, input.Alipay.GatewayURL)
		mergeString(&stored.Alipay.ReturnURL, input.Alipay.ReturnURL)
	}
	if provider == "wechat" && input.Wechat != nil {
		if stored.Wechat == nil {
			stored.Wechat = &wechatConfig{}
		}
		mergeString(&stored.Wechat.MchID, input.Wechat.MchID)
		mergeString(&stored.Wechat.AppID, input.Wechat.AppID)
		mergeString(&stored.Wechat.MerchantSerialNo, input.Wechat.MerchantSerialNo)
		mergeString(&stored.Wechat.MerchantPrivateKeyPEM, input.Wechat.MerchantPrivateKeyPEM)
		mergeString(&stored.Wechat.APIv3Key, input.Wechat.APIv3Key)
		mergeString(&stored.Wechat.PlatformPublicKeyPEM, input.Wechat.PlatformPublicKeyPEM)
		mergeString(&stored.Wechat.PlatformSerialNo, input.Wechat.PlatformSerialNo)
	}
	return stored
}

func mergeString(target *string, update string) {
	if update != "" {
		*target = update
	}
}

func (s *Service) deleteChannel(w http.ResponseWriter, r *http.Request, idText string) {
	p := currentPrincipal(r)
	if !canManagePayments(p) {
		writeError(w, http.StatusForbidden, 40301, "payment operator role required", requestID(r))
		return
	}
	channelID, err := uuid.Parse(idText)
	if err != nil {
		writeError(w, http.StatusBadRequest, 40002, "invalid channel id", requestID(r))
		return
	}
	var channel PaymentChannel
	if err := s.db.DB().WithContext(r.Context()).Take(&channel, "id = ?", channelID).Error; err != nil {
		writeError(w, http.StatusNotFound, 40401, "channel not found", requestID(r))
		return
	}
	if !s.mayAccessAccount(p, channel.AccountID) {
		writeError(w, http.StatusForbidden, 40301, "account access denied", requestID(r))
		return
	}
	var billCount int64
	if err := s.db.DB().WithContext(r.Context()).Model(&Bill{}).Where("channel_id = ?", channelID).Count(&billCount).Error; err != nil {
		writeError(w, 500, 50001, "cannot inspect channel usage", requestID(r))
		return
	}
	if billCount > 0 {
		writeError(w, http.StatusConflict, 40901, "cannot delete a channel with payment bills", requestID(r))
		return
	}
	if err := s.db.DB().WithContext(r.Context()).Delete(&channel).Error; err != nil {
		writeError(w, 500, 50001, "cannot delete payment channel", requestID(r))
		return
	}
	s.audit(r.Context(), &channel.AccountID, &p.UserID, "channel.delete", "payment_channel", channelID.String(), requestID(r), map[string]string{"provider": channel.Provider})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) listBills(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.scopedAccount(w, r)
	if !ok {
		return
	}
	query := s.db.DB().WithContext(r.Context()).Order("created_at DESC").Limit(100)
	if accountID != nil {
		query = query.Where("account_id = ?", *accountID)
	}
	if status := r.URL.Query().Get("status"); status != "" {
		query = query.Where("status = ?", status)
	}
	var models []Bill
	if err := query.Find(&models).Error; err != nil {
		writeError(w, 500, 50001, "cannot load bills", requestID(r))
		return
	}
	items := make([]map[string]any, 0)
	for _, model := range models {
		items = append(items, billPublic(billRecordFromModel(model)))
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Service) getBill(w http.ResponseWriter, r *http.Request, idText string) {
	id, err := uuid.Parse(idText)
	if err != nil {
		writeError(w, 400, 40002, "invalid bill id", requestID(r))
		return
	}
	bill, err := s.billByID(r.Context(), id)
	if err != nil {
		writeError(w, 404, 40401, "bill not found", requestID(r))
		return
	}
	if !s.mayAccessAccount(currentPrincipal(r), bill.AccountID) {
		writeError(w, 403, 40301, "account access denied", requestID(r))
		return
	}
	writeJSON(w, 200, map[string]any{"data": billPublic(bill)})
}
func (s *Service) createRefund(w http.ResponseWriter, r *http.Request, billText string) {
	p := currentPrincipal(r)
	if !canManagePayments(p) {
		writeError(w, 403, 40301, "payment operator role required", requestID(r))
		return
	}
	id, err := uuid.Parse(billText)
	if err != nil {
		writeError(w, 400, 40002, "invalid bill id", requestID(r))
		return
	}
	bill, err := s.billByID(r.Context(), id)
	if err != nil {
		writeError(w, 404, 40401, "bill not found", requestID(r))
		return
	}
	if !s.mayAccessAccount(p, bill.AccountID) {
		writeError(w, 403, 40301, "account access denied", requestID(r))
		return
	}
	if bill.Status != "paid" && bill.Status != "refunding" {
		writeError(w, 409, 40002, "only paid bill can be refunded", requestID(r))
		return
	}
	var input struct {
		RefundOrderNo string `json:"refund_order_no"`
		Amount        string `json:"amount"`
		Reason        string `json:"reason"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	amount, err := parseAmount(input.Amount)
	if err != nil || amount < 1 || amount > bill.AmountMinor {
		writeError(w, 400, 40004, "invalid refund amount", requestID(r))
		return
	}
	if input.RefundOrderNo == "" {
		writeError(w, 400, 40002, "refund_order_no is required", requestID(r))
		return
	}
	refund := refundRecord{ID: uuid.New(), AccountID: bill.AccountID, BillID: bill.ID, RefundOrderNo: input.RefundOrderNo, AmountMinor: amount, Reason: input.Reason, Status: "pending", CreatedAt: nowUTC()}
	err = s.db.DB().WithContext(r.Context()).Create(refund.toModel()).Error
	if err != nil {
		writeError(w, 409, 40006, "duplicate refund order number", requestID(r))
		return
	}
	channel, err := s.channelByID(r.Context(), bill.ChannelID)
	if err != nil {
		writeError(w, 500, 50001, "channel not found", requestID(r))
		return
	}
	tradeID, payload, err := s.refundPayment(r.Context(), channel, bill, refund)
	if err != nil {
		_ = s.db.DB().WithContext(r.Context()).Model(&Refund{}).Where("id = ?", refund.ID).Update("status", "failed").Error
		writeError(w, 502, 50001, "refund channel request failed", requestID(r))
		return
	}
	data, _ := json.Marshal(payload)
	_ = s.db.DB().WithContext(r.Context()).Model(&Refund{}).Where("id = ?", refund.ID).Updates(map[string]any{"status": "succeeded", "provider_refund_id": nullableString(tradeID), "provider_payload": string(data)}).Error
	_ = s.db.DB().WithContext(r.Context()).Model(&Bill{}).Where("id = ?", bill.ID).Update("status", "refunded").Error
	s.audit(r.Context(), &bill.AccountID, &p.UserID, "bill.refund", "bill", bill.ID.String(), requestID(r), map[string]any{"refund_order_no": input.RefundOrderNo, "amount": input.Amount})
	writeJSON(w, 201, map[string]any{"id": refund.ID, "status": "succeeded"})
}
func (s *Service) closeBill(w http.ResponseWriter, r *http.Request, billText string) {
	p := currentPrincipal(r)
	if !canManagePayments(p) {
		writeError(w, 403, 40301, "payment operator role required", requestID(r))
		return
	}
	id, err := uuid.Parse(billText)
	if err != nil {
		writeError(w, 400, 40002, "invalid bill id", requestID(r))
		return
	}
	bill, err := s.billByID(r.Context(), id)
	if err != nil {
		writeError(w, 404, 40401, "bill not found", requestID(r))
		return
	}
	if !s.mayAccessAccount(p, bill.AccountID) {
		writeError(w, 403, 40301, "account access denied", requestID(r))
		return
	}
	if bill.Status != "pending" {
		writeError(w, 409, 40002, "only pending bill can be closed", requestID(r))
		return
	}
	channel, err := s.channelByID(r.Context(), bill.ChannelID)
	if err != nil {
		writeError(w, 500, 50001, "channel not found", requestID(r))
		return
	}
	if err = s.closePayment(r.Context(), channel, bill); err != nil {
		writeError(w, 502, 50001, "close channel request failed", requestID(r))
		return
	}
	_ = s.db.DB().WithContext(r.Context()).Model(&Bill{}).Where("id = ? AND status = ?", bill.ID, "pending").Updates(map[string]any{"status": "closed", "closed_at": nowUTC()}).Error
	s.audit(r.Context(), &bill.AccountID, &p.UserID, "bill.close", "bill", bill.ID.String(), requestID(r), map[string]any{})
	w.WriteHeader(http.StatusNoContent)
}
func (s *Service) reconcileBill(w http.ResponseWriter, r *http.Request, billText string) {
	p := currentPrincipal(r)
	if !canManagePayments(p) {
		writeError(w, http.StatusForbidden, 40301, "payment operator role required", requestID(r))
		return
	}
	id, err := uuid.Parse(billText)
	if err != nil {
		writeError(w, http.StatusBadRequest, 40002, "invalid bill id", requestID(r))
		return
	}
	var input struct {
		ProviderTransactionID string `json:"provider_transaction_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ProviderTransactionID = strings.TrimSpace(input.ProviderTransactionID)
	if input.ProviderTransactionID == "" {
		writeError(w, http.StatusBadRequest, 40002, "provider_transaction_id is required", requestID(r))
		return
	}
	var bill billRecord
	err = s.db.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var model Bill
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&model, "id = ?", id).Error; err != nil {
			return err
		}
		bill = billRecordFromModel(model)
		if !s.mayAccessAccount(p, bill.AccountID) {
			return clientError{40301, "account access denied"}
		}
		if bill.Status != "pending" {
			return clientError{40002, "only pending bill can be reconciled"}
		}
		paidAt := nowUTC()
		payload, _ := json.Marshal(map[string]any{"reconciled": true, "provider_transaction_id": input.ProviderTransactionID})
		if err := tx.Model(&Bill{}).Where("id = ? AND status = ?", bill.ID, "pending").Updates(map[string]any{"status": "paid", "provider_transaction_id": input.ProviderTransactionID, "provider_payload": string(payload), "paid_at": paidAt}).Error; err != nil {
			return err
		}
		bill.Status, bill.ProviderTransactionID, bill.PaidAt = "paid", input.ProviderTransactionID, &paidAt
		return nil
	})
	if err != nil {
		if client, ok := err.(clientError); ok {
			status := http.StatusConflict
			if client.Code == 40301 {
				status = http.StatusForbidden
			}
			writeError(w, status, client.Code, client.Message, requestID(r))
			return
		}
		if isNoRows(err) {
			writeError(w, http.StatusNotFound, 40401, "bill not found", requestID(r))
			return
		}
		writeError(w, http.StatusInternalServerError, 50001, "cannot reconcile bill", requestID(r))
		return
	}
	s.audit(r.Context(), &bill.AccountID, &p.UserID, "bill.reconcile", "bill", bill.ID.String(), requestID(r), map[string]string{"provider_transaction_id": input.ProviderTransactionID})
	go s.notifyMerchant(context.Background(), bill)
	writeJSON(w, http.StatusOK, map[string]any{"data": billPublic(bill)})
}

func (s *Service) retryMerchantCallback(w http.ResponseWriter, r *http.Request, billText string) {
	p := currentPrincipal(r)
	if !canManagePayments(p) {
		writeError(w, http.StatusForbidden, 40301, "payment operator role required", requestID(r))
		return
	}
	id, err := uuid.Parse(billText)
	if err != nil {
		writeError(w, http.StatusBadRequest, 40002, "invalid bill id", requestID(r))
		return
	}
	bill, err := s.billByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, 40401, "bill not found", requestID(r))
		return
	}
	if !s.mayAccessAccount(p, bill.AccountID) {
		writeError(w, http.StatusForbidden, 40301, "account access denied", requestID(r))
		return
	}
	if bill.Status != "paid" {
		writeError(w, http.StatusConflict, 40002, "only paid bill callback can be retried", requestID(r))
		return
	}
	s.audit(r.Context(), &bill.AccountID, &p.UserID, "bill.callback_retry", "bill", bill.ID.String(), requestID(r), map[string]any{})
	go s.notifyMerchant(context.Background(), bill)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Service) listRefunds(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.scopedAccount(w, r)
	if !ok {
		return
	}
	query := s.db.DB().WithContext(r.Context()).Order("created_at DESC").Limit(100)
	if accountID != nil {
		query = query.Where("account_id = ?", *accountID)
	}
	var refunds []Refund
	if err := query.Find(&refunds).Error; err != nil {
		writeError(w, 500, 50001, "cannot load refunds", requestID(r))
		return
	}
	items := make([]map[string]any, 0)
	for _, item := range refunds {
		items = append(items, map[string]any{"id": item.ID, "bill_id": item.BillID, "refund_order_no": item.RefundOrderNo, "amount": moneyString(item.AmountMinor), "reason": item.Reason, "status": item.Status, "provider_refund_id": item.ProviderRefundID, "created_at": item.CreatedAt})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Service) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.scopedAccount(w, r)
	if !ok {
		return
	}
	query := s.db.DB().WithContext(r.Context()).Order("created_at DESC").Limit(100)
	if accountID != nil {
		query = query.Where("account_id = ?", *accountID)
	}
	var logs []AuditLog
	if err := query.Find(&logs).Error; err != nil {
		writeError(w, 500, 50001, "cannot load audit logs", requestID(r))
		return
	}
	items := make([]map[string]any, 0)
	for _, log := range logs {
		var parsed any
		_ = json.Unmarshal([]byte(log.Detail), &parsed)
		items = append(items, map[string]any{"id": log.ID, "account_id": log.AccountID, "actor_user_id": log.ActorUserID, "action": log.Action, "target_type": log.TargetType, "target_id": log.TargetID, "request_id": log.RequestID, "detail": parsed, "created_at": log.CreatedAt})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Service) alipayWebhook(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	s.processWebhook(w, r, r.PathValue("token"), r.PostForm)
}
func (s *Service) wechatWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	s.processWechatWebhook(w, r, r.PathValue("token"), body)
}
func (s *Service) processWebhook(w http.ResponseWriter, r *http.Request, token string, form url.Values) {
	channel, err := s.loadChannel(r.Context(), token)
	if err != nil || !channel.Enabled {
		http.Error(w, "not found", 404)
		return
	}
	result, err := s.verifyWebhook(channel, r.Header, nil, form)
	if err != nil {
		s.logger.Warn("alipay webhook rejected", "error", err)
		http.Error(w, "invalid signature", 401)
		return
	}
	if err = s.applyWebhook(r.Context(), channel, result); err != nil {
		s.logger.Error("webhook processing failed", "error", err)
		http.Error(w, "retry", 500)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("success"))
}
func (s *Service) processWechatWebhook(w http.ResponseWriter, r *http.Request, token string, body []byte) {
	channel, err := s.loadChannel(r.Context(), token)
	if err != nil || !channel.Enabled {
		http.Error(w, "not found", 404)
		return
	}
	result, err := s.verifyWebhook(channel, r.Header, body, nil)
	if err != nil {
		s.logger.Warn("wechat webhook rejected", "error", err)
		http.Error(w, "invalid signature", 401)
		return
	}
	if err = s.applyWebhook(r.Context(), channel, result); err != nil {
		s.logger.Error("webhook processing failed", "error", err)
		http.Error(w, "retry", 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Service) applyWebhook(ctx context.Context, channel channelRecord, result webhookResult) error {
	raw, _ := json.Marshal(result.Raw)
	var bill billRecord
	notify := false
	err := s.db.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		event := WebhookEvent{ID: uuid.New(), AccountID: channel.AccountID, ChannelID: channel.ID, Provider: channel.Provider, EventKey: result.EventKey, Verified: true, Payload: string(raw), ProcessedAt: timePtr(nowUTC())}
		if err := tx.Create(&event).Error; err != nil {
			if isUniqueViolation(err) {
				return nil
			}
			return err
		}
		if !result.Paid {
			return nil
		}
		var model Bill
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND merchant_order_no = ?", channel.AccountID, result.MerchantOrderNo).Take(&model).Error; err != nil {
			return err
		}
		bill = billRecordFromModel(model)
		if bill.Status != "pending" {
			return nil
		}
		paid := nowUTC()
		if err := tx.Model(&Bill{}).Where("id = ?", bill.ID).Updates(map[string]any{"status": "paid", "provider_transaction_id": nullableString(result.ProviderTradeID), "provider_payload": string(raw), "paid_at": paid}).Error; err != nil {
			return err
		}
		bill.Status, bill.ProviderTransactionID, bill.PaidAt, notify = "paid", result.ProviderTradeID, &paid, true
		return nil
	})
	if err != nil {
		return err
	}
	if notify {
		go s.notifyMerchant(context.Background(), bill)
	}
	return nil
}
func (s *Service) notifyMerchant(ctx context.Context, bill billRecord) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var account Account
	if err := s.db.DB().WithContext(ctx).Select("api_secret_ciphertext").Take(&account, "id = ?", bill.AccountID).Error; err != nil {
		s.logger.Warn("merchant callback cannot load account", "bill_id", bill.ID, "error", err)
		return
	}
	// EasyPay-compatible notifications use the same merchant key as payment requests.
	secret, err := s.decrypt(account.APISecretCiphertext)
	if err != nil {
		s.logger.Error("merchant callback cannot decrypt key", "bill_id", bill.ID, "error", err)
		return
	}
	values := url.Values{"pid": {s.merchantNo(ctx, bill.AccountID)}, "type": {providerOPSCode(bill.Provider)}, "out_trade_no": {bill.MerchantOrderNo}, "trade_no": {bill.ProviderTransactionID}, "name": {bill.Subject}, "money": {moneyString(bill.AmountMinor)}, "trade_status": {"TRADE_SUCCESS"}, "param": {bill.Metadata}, "sign_type": {"MD5"}}
	signature, _ := signOPS(canonicalSignatureValues(values), secret, "MD5")
	values.Set("sign", signature)
	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	var lastErr error
	statusCode := 0
	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, bill.NotifyURL, strings.NewReader(values.Encode()))
		if err != nil {
			lastErr = err
			break
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err == nil && resp != nil {
			statusCode = resp.StatusCode
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
			if statusCode/100 == 2 {
				s.logger.Info("merchant callback delivered", "bill_id", bill.ID, "attempt", attempt, "status", statusCode)
				s.audit(ctx, &bill.AccountID, nil, "merchant.callback.delivered", "bill", bill.ID.String(), "", map[string]any{"attempt": attempt, "status": statusCode})
				return
			}
			lastErr = fmt.Errorf("merchant callback responded with HTTP %d", statusCode)
		} else {
			lastErr = err
		}
		if attempt < 3 {
			select {
			case <-ctx.Done():
				attempt = 3
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
	}
	s.logger.Warn("merchant callback failed", "bill_id", bill.ID, "status", statusCode, "error", lastErr)
	s.audit(ctx, &bill.AccountID, nil, "merchant.callback.failed", "bill", bill.ID.String(), "", map[string]any{"attempts": 3, "status": statusCode, "error": errorMessage(lastErr)})
}

func errorMessage(err error) string {
	if err == nil {
		return "unknown callback failure"
	}
	return err.Error()
}
func (s *Service) merchantNo(ctx context.Context, accountID uuid.UUID) string {
	var account Account
	_ = s.db.DB().WithContext(ctx).Select("merchant_no").Take(&account, "id = ?", accountID).Error
	return account.MerchantNo
}

func (s *Service) accountByMerchantNo(ctx context.Context, no string) (accountRecord, error) {
	var model Account
	err := s.db.DB().WithContext(ctx).Where("merchant_no = ? AND status = ?", no, "active").Take(&model).Error
	if err != nil {
		return accountRecord{}, err
	}
	account := accountRecord{ID: model.ID, Name: model.Name, MerchantNo: model.MerchantNo, Status: model.Status, CreatedAt: model.CreatedAt}
	account.APISecret, err = s.decrypt(model.APISecretCiphertext)
	if err != nil {
		return account, err
	}
	account.CallbackSecret, err = s.decrypt(model.CallbackSecretCiphertext)
	return account, err
}
func (s *Service) channelByID(ctx context.Context, id uuid.UUID) (channelRecord, error) {
	var model PaymentChannel
	err := s.db.DB().WithContext(ctx).Take(&model, "id = ?", id).Error
	if err != nil {
		return channelRecord{}, err
	}
	c := channelRecord{ID: model.ID, AccountID: model.AccountID, Provider: model.Provider, DisplayName: model.DisplayName, Enabled: model.Enabled, WebhookToken: model.WebhookToken}
	plain, err := s.decrypt(model.ConfigCiphertext)
	if err != nil {
		return c, err
	}
	if plain != "" {
		err = json.Unmarshal([]byte(plain), &c.Config)
	}
	return c, err
}
func (s *Service) billByID(ctx context.Context, id uuid.UUID) (billRecord, error) {
	var model Bill
	err := s.db.DB().WithContext(ctx).Take(&model, "id = ?", id).Error
	return billRecordFromModel(model), err
}
func (s *Service) billByMerchantOrder(ctx context.Context, accountID uuid.UUID, orderNo string) (billRecord, error) {
	var model Bill
	err := s.db.DB().WithContext(ctx).Where("account_id = ? AND merchant_order_no = ?", accountID, orderNo).Take(&model).Error
	return billRecordFromModel(model), err
}
func billPublic(b billRecord) map[string]any {
	return map[string]any{"id": b.ID, "platform_order_no": b.PlatformOrderNo, "merchant_order_no": b.MerchantOrderNo, "subject": b.Subject, "description": b.Description, "amount": moneyString(b.AmountMinor), "currency": b.Currency, "provider": b.Provider, "scene": b.Scene, "status": b.Status, "provider_transaction_id": b.ProviderTransactionID, "notify_url": b.NotifyURL, "return_url": b.ReturnURL, "metadata": b.Metadata, "expires_at": b.ExpiresAt, "paid_at": b.PaidAt, "closed_at": b.ClosedAt, "created_at": b.CreatedAt, "updated_at": b.UpdatedAt}
}
func (s *Service) mayAccessAccount(p principal, accountID uuid.UUID) bool {
	return p.AccountID != nil && *p.AccountID == accountID
}
func canManagePayments(p principal) bool {
	return (p.Role == "user" || p.Role == "platform_admin") && p.AccountID != nil
}
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}
func (s *Service) validCallbackURL(raw string) bool {
	return s.validExternalURL(raw)
}
func (s *Service) validExternalURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	if s.environment == "production" && parsed.Scheme != "https" {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return s.environment != "production"
	}
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return s.environment != "production"
	}
	if s.environment == "production" && ip == nil {
		resolved, err := net.LookupIP(host)
		if err != nil || len(resolved) == 0 {
			return false
		}
		for _, address := range resolved {
			if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() {
				return false
			}
		}
	}
	return parsed.Scheme == "https" || parsed.Scheme == "http"
}
func (s *Service) normalizedOIDCIssuer(raw string) (string, error) {
	issuer := strings.TrimRight(strings.TrimSpace(raw), "/")
	if !s.validExternalURL(issuer) {
		return "", errors.New("invalid issuer URL")
	}
	parsed, _ := url.Parse(issuer)
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("issuer URL must not contain a query or fragment")
	}
	return issuer, nil
}
func (in *paymentInput) normalize() {
	if in.MerchantID == "" {
		in.MerchantID = in.value("pid")
	}
	in.PaymentMethod = strings.ToLower(strings.TrimSpace(in.PaymentMethod))
	in.MerchantOrderNo = strings.TrimSpace(in.MerchantOrderNo)
	in.Subject = strings.TrimSpace(in.Subject)
	in.Amount = strings.TrimSpace(in.Amount)
	in.SignType = strings.TrimSpace(in.SignType)
}
func (in paymentInput) value(_ string) string { return "" }
func inputFromValues(values url.Values) paymentInput {
	return paymentInput{MerchantID: first(values, "merchant_id", "pid", "mch_id"), PaymentMethod: first(values, "payment_method", "type"), MerchantOrderNo: first(values, "merchant_order_no", "out_trade_no"), Subject: first(values, "subject", "name"), Description: values.Get("description"), Amount: first(values, "amount", "money"), NotifyURL: values.Get("notify_url"), ReturnURL: values.Get("return_url"), Metadata: first(values, "metadata", "param"), Scene: first(values, "scene", "device"), SignType: values.Get("sign_type"), Sign: values.Get("sign"), signatureValues: values}
}
func first(values url.Values, keys ...string) string {
	for _, key := range keys {
		if value := values.Get(key); value != "" {
			return value
		}
	}
	return ""
}
func verifyOPSSignature(input paymentInput, secret string) error {
	legacyValues := url.Values{"pid": {input.MerchantID}, "type": {providerOPSCode(input.PaymentMethod)}, "out_trade_no": {input.MerchantOrderNo}, "name": {input.Subject}, "money": {input.Amount}, "notify_url": {input.NotifyURL}, "return_url": {input.ReturnURL}, "param": {input.Metadata}, "device": {input.Scene}}
	modernValues := url.Values{"merchant_id": {input.MerchantID}, "payment_method": {input.PaymentMethod}, "merchant_order_no": {input.MerchantOrderNo}, "subject": {input.Subject}, "description": {input.Description}, "amount": {input.Amount}, "notify_url": {input.NotifyURL}, "return_url": {input.ReturnURL}, "metadata": {input.Metadata}, "scene": {input.Scene}}
	algorithms := []string{strings.ToUpper(input.SignType)}
	if input.SignType == "" {
		// Existing EasyPay clients often omit sign_type and use MD5, while OPS
		// clients use HMAC-SHA256. Both are deterministic with the same key.
		algorithms = []string{"MD5", "HMAC-SHA256"}
	}
	canonicals := []string{canonicalValues(legacyValues), canonicalValues(modernValues)}
	if len(input.signatureValues) > 0 {
		canonicals = append(canonicals, canonicalSignatureValues(input.signatureValues))
	}
	for _, algorithm := range algorithms {
		for _, canonical := range canonicals {
			expected, err := signOPS(canonical, secret, algorithm)
			if err != nil {
				return err
			}
			if hmac.Equal([]byte(strings.ToLower(expected)), []byte(strings.ToLower(input.Sign))) {
				return nil
			}
		}
	}
	return errors.New("signature mismatch")
}

func canonicalSignatureValues(values url.Values) string {
	filtered := make(url.Values, len(values))
	for key, raw := range values {
		if key == "sign" || key == "sign_type" || len(raw) == 0 || raw[0] == "" {
			continue
		}
		filtered[key] = raw
	}
	return canonicalValues(filtered)
}

func signOPS(canonical, secret, signType string) (string, error) {
	switch signType {
	case "MD5":
		sum := md5.Sum([]byte(canonical + secret))
		return hex.EncodeToString(sum[:]), nil
	case "HMAC-SHA256":
		return signHMAC(canonical, secret), nil
	default:
		return "", errors.New("unsupported sign type")
	}
}
func signHMAC(message, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}
func providerOPSCode(provider string) string {
	if provider == "wechat" {
		return "wxpay"
	}
	return provider
}
func validateProviderConfig(provider string, config providerConfig) error {
	if provider == "alipay" {
		if config.Alipay == nil || config.Alipay.PID == "" || config.Alipay.AppID == "" || config.Alipay.AppPrivateKeyPEM == "" || config.Alipay.AlipayPublicKeyPEM == "" {
			return errors.New("支付宝配置需要 pid、app_id、app_private_key_pem、alipay_public_key_pem")
		}
		if mode := alipayPaymentMode(config.Alipay); mode != "face_to_face" && mode != "website" {
			return errors.New("支付宝支付方式必须为 face_to_face 或 website")
		}
		return nil
	}
	if provider == "wechat" {
		if config.Wechat == nil || config.Wechat.MchID == "" || config.Wechat.AppID == "" || config.Wechat.MerchantSerialNo == "" || config.Wechat.MerchantPrivateKeyPEM == "" || config.Wechat.APIv3Key == "" {
			return errors.New("微信支付配置需要 mch_id、app_id、merchant_serial_no、merchant_private_key_pem、api_v3_key")
		}
		if len(config.Wechat.APIv3Key) != 32 {
			return errors.New("微信 APIv3 密钥必须为 32 字节")
		}
		return nil
	}
	return errors.New("unsupported provider")
}

type clientError struct {
	Code    int
	Message string
}

func (e clientError) Error() string { return e.Message }

type providerError struct{ cause error }

func (e providerError) Error() string { return e.cause.Error() }

func writePublicError(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	if errors.As(err, &ce) {
		writeError(w, 400, ce.Code, ce.Message, requestID(r))
		return
	}
	var pe providerError
	if errors.As(err, &pe) {
		message := pe.Error()
		if len(message) > 800 {
			message = message[:800]
		}
		writeError(w, http.StatusBadGateway, 50001, message, requestID(r))
		return
	}
	writeError(w, 502, 50001, "payment service unavailable", requestID(r))
}

var errSetupComplete = errors.New("initial setup already complete")

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func timePtr(value time.Time) *time.Time { return &value }

func billRecordFromModel(model Bill) billRecord {
	return billRecord{ID: model.ID, AccountID: model.AccountID, ChannelID: model.ChannelID, PlatformOrderNo: model.PlatformOrderNo, MerchantOrderNo: model.MerchantOrderNo, Subject: model.Subject, Description: model.Description, AmountMinor: model.AmountMinor, Currency: model.Currency, Provider: model.Provider, Scene: model.Scene, Status: model.Status, ProviderTransactionID: model.ProviderTransactionID, NotifyURL: model.NotifyURL, ReturnURL: stringValue(model.ReturnURL), Metadata: stringValue(model.Metadata), ExpiresAt: model.ExpiresAt, PaidAt: model.PaidAt, ClosedAt: model.ClosedAt, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt}
}
func (record billRecord) toModel() *Bill {
	return &Bill{ID: record.ID, AccountID: record.AccountID, ChannelID: record.ChannelID, PlatformOrderNo: record.PlatformOrderNo, MerchantOrderNo: record.MerchantOrderNo, Subject: record.Subject, Description: record.Description, AmountMinor: record.AmountMinor, Currency: record.Currency, Provider: record.Provider, Scene: record.Scene, Status: record.Status, NotifyURL: record.NotifyURL, ReturnURL: nullableString(record.ReturnURL), Metadata: nullableString(record.Metadata), ExpiresAt: record.ExpiresAt, PaidAt: record.PaidAt, ClosedAt: record.ClosedAt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}
func (record refundRecord) toModel() *Refund {
	return &Refund{ID: record.ID, AccountID: record.AccountID, BillID: record.BillID, RefundOrderNo: record.RefundOrderNo, AmountMinor: record.AmountMinor, Reason: record.Reason, Status: record.Status, ProviderRefundID: record.ProviderRefundID, CreatedAt: record.CreatedAt}
}
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func _unused(_ context.Context, _ strconv.NumError) {}
