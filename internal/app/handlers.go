package app

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type tenantRecord struct {
	ID                                                  uuid.UUID
	Name, MerchantNo, Status, APISecret, CallbackSecret string
	CreatedAt                                           time.Time
}
type billRecord struct {
	ID, TenantID, ChannelID                                uuid.UUID
	PlatformOrderNo, MerchantOrderNo, Subject, Description string
	AmountMinor                                            int64
	Currency, Provider, Scene, Status                      string
	ProviderTransactionID                                  string
	NotifyURL, ReturnURL, Metadata                         string
	ExpiresAt, PaidAt, ClosedAt                            *time.Time
	CreatedAt, UpdatedAt                                   time.Time
}
type refundRecord struct {
	ID, TenantID, BillID             uuid.UUID
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
	SiteKey   string `json:"site_key"`
	SecretKey string `json:"secret_key"`
}
type oidcSiteConfig struct {
	IssuerURL    string `json:"issuer_url"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RedirectURL  string `json:"redirect_url"`
}
type accountSiteSettings struct {
	Email    smtpSiteConfig
	HCaptcha hcaptchaSiteConfig
	OIDC     oidcSiteConfig
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
}
type paymentResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		PlatformOrderNo string     `json:"platform_order_no"`
		MerchantOrderNo string     `json:"merchant_order_no"`
		PayURL          string     `json:"pay_url,omitempty"`
		QRCode          string     `json:"qrcode,omitempty"`
		ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	} `json:"data"`
}

func (s *Service) discovery(w http.ResponseWriter, r *http.Request) {
	base := s.baseURL
	writeJSON(w, http.StatusOK, map[string]any{
		"spec": "Open Payment Specification", "spec_version": "1.0.0", "profile": []string{"OPS-EPAY-1", "OPS-CORE-1", "OPS-EXT-1"},
		"platform":        map[string]any{"name": "Tsumugi Pay", "vendor": "tsumugi", "homepage": base, "charset": "utf-8", "timezone": "Asia/Shanghai", "currency": "CNY"},
		"endpoints":       map[string]any{"submit": base + "/submit.php", "mapi": base + "/mapi.php", "api": base + "/api.php", "query": base + "/api.php?act=order", "refund": base + "/api.php?act=refund", "close": base + "/api.php?act=close"},
		"transports":      map[string]any{"payment_create": []string{"form_post", "json"}, "query": []string{"form_get", "form_post"}, "refund": []string{"json"}, "notify": []string{"form_post"}},
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
func (s *Service) legacyCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, 400, 40002, "invalid form", requestID(r))
		return
	}
	input := inputFromValues(r.PostForm)
	response, err := s.createBill(r.Context(), input)
	if err != nil {
		writePublicError(w, r, err)
		return
	}
	if response.Data.PayURL != "" {
		http.Redirect(w, r, response.Data.PayURL, http.StatusSeeOther)
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
	tenant, err := s.tenantByMerchantNo(ctx, input.MerchantID)
	if err != nil {
		if isNoRows(err) {
			return paymentResponse{}, clientError{40005, "merchant not found or disabled"}
		}
		return paymentResponse{}, err
	}
	if err := verifyOPSSignature(input, tenant.APISecret); err != nil {
		return paymentResponse{}, clientError{40001, "invalid signature"}
	}
	var channelID uuid.UUID
	var enabled bool
	var configCiphertext, displayName, token string
	err = s.db.QueryRow(ctx, `SELECT id,enabled,config_ciphertext,display_name,webhook_token FROM payment_channels WHERE tenant_id=$1 AND provider=$2`, tenant.ID, input.PaymentMethod).Scan(&channelID, &enabled, &configCiphertext, &displayName, &token)
	if err != nil {
		return paymentResponse{}, err
	}
	if !enabled {
		return paymentResponse{}, clientError{40003, "payment channel is disabled"}
	}
	plain, err := s.decrypt(configCiphertext)
	if err != nil {
		return paymentResponse{}, err
	}
	channel := channelRecord{ID: channelID, TenantID: tenant.ID, Provider: input.PaymentMethod, Enabled: enabled, DisplayName: displayName, WebhookToken: token}
	if plain != "" {
		if err = json.Unmarshal([]byte(plain), &channel.Config); err != nil {
			return paymentResponse{}, errors.New("stored channel config is invalid")
		}
	}
	scene := input.Scene
	if scene == "" {
		if input.PaymentMethod == "alipay" {
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
	bill := billRecord{ID: uuid.New(), TenantID: tenant.ID, ChannelID: channelID, PlatformOrderNo: "TP" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))[:24], MerchantOrderNo: input.MerchantOrderNo, Subject: input.Subject, Description: input.Description, AmountMinor: amount, Currency: "CNY", Provider: input.PaymentMethod, Scene: scene, Status: "pending", NotifyURL: input.NotifyURL, ReturnURL: input.ReturnURL, Metadata: input.Metadata, ExpiresAt: &expires, CreatedAt: nowUTC(), UpdatedAt: nowUTC()}
	_, err = s.db.Exec(ctx, `INSERT INTO bills (id,tenant_id,channel_id,platform_order_no,merchant_order_no,subject,description,amount_minor,currency,provider,scene,status,notify_url,return_url,metadata,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'pending',$12,$13,$14,$15)`, bill.ID, bill.TenantID, bill.ChannelID, bill.PlatformOrderNo, bill.MerchantOrderNo, bill.Subject, bill.Description, bill.AmountMinor, bill.Currency, bill.Provider, bill.Scene, bill.NotifyURL, nullString(bill.ReturnURL), nullString(bill.Metadata), bill.ExpiresAt)
	if err != nil {
		if strings.Contains(err.Error(), "bills_tenant_id_merchant_order_no_key") {
			return paymentResponse{}, clientError{40006, "duplicate merchant order number"}
		}
		return paymentResponse{}, err
	}
	result, err := s.createPayment(ctx, channel, bill)
	if err != nil {
		_, _ = s.db.Exec(ctx, `UPDATE bills SET status='failed',updated_at=NOW() WHERE id=$1`, bill.ID)
		return paymentResponse{}, fmt.Errorf("payment channel request failed: %w", err)
	}
	payload, _ := json.Marshal(result.Raw)
	_, err = s.db.Exec(ctx, `UPDATE bills SET provider_transaction_id=$2,provider_payload=$3,updated_at=NOW() WHERE id=$1`, bill.ID, nullString(result.ProviderTransactionID), payload)
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
	response.Data.ExpiresAt = bill.ExpiresAt
	return response, nil
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
	tenant, err := s.tenantByMerchantNo(r.Context(), input.MerchantID)
	if err != nil {
		writeError(w, 404, 40005, "merchant not found or disabled", requestID(r))
		return
	}
	if err := verifyOPSSignature(input, tenant.APISecret); err != nil {
		writeError(w, 401, 40001, "invalid signature", requestID(r))
		return
	}
	bill, err := s.billByMerchantOrder(r.Context(), tenant.ID, input.MerchantOrderNo)
	if err != nil {
		writeError(w, 404, 40401, "order not found", requestID(r))
		return
	}
	writeJSON(w, 200, map[string]any{"code": 0, "message": "success", "data": billPublic(bill)})
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var id uuid.UUID
	var tenantID *uuid.UUID
	var hash, role, name string
	var active bool
	err := s.db.QueryRow(r.Context(), `SELECT id,tenant_id,password_hash,role,display_name,is_active FROM users WHERE email=$1`, strings.ToLower(strings.TrimSpace(input.Email))).Scan(&id, &tenantID, &hash, &role, &name, &active)
	if err != nil || !active || bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) != nil {
		writeError(w, 401, 40103, "invalid email or password", requestID(r))
		return
	}
	claims := jwtClaims{Role: role, Email: strings.ToLower(strings.TrimSpace(input.Email)), RegisteredClaims: jwt.RegisteredClaims{Subject: id.String(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())}}
	if tenantID != nil {
		claims.TenantID = tenantID.String()
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.jwtSecret)
	if err != nil {
		writeError(w, 500, 50001, "cannot issue access token", requestID(r))
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": signed, "token_type": "Bearer", "expires_in": 28800, "user": map[string]any{"id": id, "email": claims.Email, "display_name": name, "role": role, "tenant_id": tenantID}})
}

// setupStatus is deliberately minimal: it only reveals whether an initial
// user needs to be created, never any account metadata.
func (s *Service) setupStatus(w http.ResponseWriter, r *http.Request) {
	var users int
	if err := s.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot determine setup state", requestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"required": users == 0})
}

// setupInitialize creates the one and only first platform administrator. A
// database guard makes this endpoint single-use even if two browser tabs race.
func (s *Service) setupInitialize(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Password    string `json:"password"`
		AccountName string `json:"account_name"`
		MerchantNo  string `json:"merchant_no"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.AccountName = strings.TrimSpace(input.AccountName)
	input.MerchantNo = strings.TrimSpace(input.MerchantNo)
	if input.DisplayName == "" || input.Email == "" || input.AccountName == "" || len(input.Password) < 10 {
		writeError(w, http.StatusBadRequest, 40002, "account_name, display_name, email and a 10-character password are required", requestID(r))
		return
	}
	if input.MerchantNo == "" {
		input.MerchantNo = "M" + strings.ToUpper(randomToken(8))
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot secure password", requestID(r))
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot initialize setup", requestID(r))
		return
	}
	defer tx.Rollback(r.Context())
	var existingUsers int
	if err = tx.QueryRow(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&existingUsers); err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot determine setup state", requestID(r))
		return
	}
	if existingUsers != 0 {
		writeError(w, http.StatusConflict, 40901, "initial setup has already been completed", requestID(r))
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO system_settings (setting_key,setting_value) VALUES ($1,$2)`, "oobe_complete", nowUTC().Format(time.RFC3339)); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, 40901, "initial setup has already been completed", requestID(r))
			return
		}
		writeError(w, http.StatusInternalServerError, 50001, "cannot reserve setup", requestID(r))
		return
	}
	adminID := uuid.New()
	tenantID := uuid.New()
	apiSecret := randomToken(32)
	callbackSecret := randomToken(32)
	apiCiphertext, encryptErr := s.encrypt(apiSecret)
	callbackCiphertext, callbackEncryptErr := s.encrypt(callbackSecret)
	if encryptErr != nil || callbackEncryptErr != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot secure account credentials", requestID(r))
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO tenants (id,name,merchant_no,api_secret_ciphertext,callback_secret_ciphertext) VALUES ($1,$2,$3,$4,$5)`, tenantID, input.AccountName, input.MerchantNo, apiCiphertext, callbackCiphertext); err != nil {
		writeError(w, http.StatusConflict, 40006, "merchant number already exists", requestID(r))
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO users (id,tenant_id,email,password_hash,display_name,role) VALUES ($1,$2,$3,$4,$5,'tenant_admin')`, adminID, tenantID, input.Email, string(hash), input.DisplayName); err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot create initial user", requestID(r))
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot complete setup", requestID(r))
		return
	}
	s.audit(r.Context(), &tenantID, &adminID, "system.oobe_complete", "account", tenantID.String(), requestID(r), map[string]string{"email": input.Email, "account_name": input.AccountName})
	writeJSON(w, http.StatusCreated, map[string]any{"message": "initial user created", "user": map[string]any{"id": adminID, "email": input.Email, "display_name": input.DisplayName, "role": "tenant_admin"}, "account": map[string]any{"id": tenantID, "name": input.AccountName, "merchant_no": input.MerchantNo}, "credentials": map[string]string{"api_secret": apiSecret, "callback_secret": callbackSecret}})
}

func (s *Service) admin(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/admin")
	switch {
	case r.Method == "GET" && path == "/me":
		s.adminMe(w, r)
	case r.Method == "GET" && path == "/dashboard":
		s.dashboard(w, r)
	case path == "/tenants":
		if r.Method == "GET" {
			s.listTenants(w, r)
		} else if r.Method == "POST" {
			s.createTenant(w, r)
		} else {
			methodNotAllowed(w, r)
		}
	case strings.HasPrefix(path, "/tenants/") && r.Method == "PATCH":
		s.patchTenant(w, r, strings.TrimPrefix(path, "/tenants/"))
	case path == "/users":
		if r.Method == "GET" {
			s.listUsers(w, r)
		} else if r.Method == "POST" {
			s.createUser(w, r)
		} else {
			methodNotAllowed(w, r)
		}
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
	case path == "/bills" && r.Method == "GET":
		s.listBills(w, r)
	case strings.HasPrefix(path, "/bills/") && strings.HasSuffix(path, "/refunds") && r.Method == "POST":
		s.createRefund(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/bills/"), "/refunds"))
	case strings.HasPrefix(path, "/bills/") && strings.HasSuffix(path, "/close") && r.Method == "POST":
		s.closeBill(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/bills/"), "/close"))
	case strings.HasPrefix(path, "/bills/") && r.Method == "GET":
		s.getBill(w, r, strings.TrimPrefix(path, "/bills/"))
	case path == "/refunds" && r.Method == "GET":
		s.listRefunds(w, r)
	case path == "/audit-logs" && r.Method == "GET":
		s.listAuditLogs(w, r)
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
	response := map[string]any{"id": p.UserID, "email": p.Email, "role": p.Role, "account_id": p.TenantID}
	if p.TenantID != nil {
		var tenant struct{ Name, MerchantNo, Status string }
		if err := s.db.QueryRow(r.Context(), `SELECT name,merchant_no,status FROM tenants WHERE id=$1`, *p.TenantID).Scan(&tenant.Name, &tenant.MerchantNo, &tenant.Status); err == nil {
			response["account"] = map[string]any{"id": p.TenantID, "name": tenant.Name, "merchant_no": tenant.MerchantNo, "status": tenant.Status}
		}
	}
	writeJSON(w, 200, response)
}

func (s *Service) getSiteSettings(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.siteSettingsTenant(w, r)
	if !ok {
		return
	}
	settings, _, err := s.loadAccountSettings(r.Context(), *tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot load site settings", requestID(r))
		return
	}
	writeJSON(w, http.StatusOK, siteSettingsPublic(settings))
}

func (s *Service) patchSiteSettings(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.siteSettingsTenant(w, r)
	if !ok {
		return
	}
	var input struct {
		Email    *smtpSiteConfig     `json:"email"`
		HCaptcha *hcaptchaSiteConfig `json:"hcaptcha"`
		OIDC     *oidcSiteConfig     `json:"oidc"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	settings, exists, err := s.loadAccountSettings(r.Context(), *tenantID)
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
			if exists {
				_, err = s.db.Exec(r.Context(), `UPDATE account_settings SET email_config_ciphertext=$2,hcaptcha_config_ciphertext=$3,oidc_config_ciphertext=$4,updated_at=NOW() WHERE tenant_id=$1`, *tenantID, emailCiphertext, hcaptchaCiphertext, oidcCiphertext)
			} else {
				_, err = s.db.Exec(r.Context(), `INSERT INTO account_settings (tenant_id,email_config_ciphertext,hcaptcha_config_ciphertext,oidc_config_ciphertext) VALUES ($1,$2,$3,$4)`, *tenantID, emailCiphertext, hcaptchaCiphertext, oidcCiphertext)
			}
		}
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, 50001, "cannot save site settings", requestID(r))
		return
	}
	p := currentPrincipal(r)
	s.audit(r.Context(), tenantID, &p.UserID, "site_settings.update", "site_settings", tenantID.String(), requestID(r), map[string]bool{"email": input.Email != nil, "hcaptcha": input.HCaptcha != nil, "oidc": input.OIDC != nil})
	writeJSON(w, http.StatusOK, siteSettingsPublic(settings))
}

func (s *Service) siteSettingsTenant(w http.ResponseWriter, r *http.Request) (*uuid.UUID, bool) {
	p := currentPrincipal(r)
	if p.Role != "tenant_admin" && p.Role != "platform_admin" {
		writeError(w, http.StatusForbidden, 40301, "account owner role required", requestID(r))
		return nil, false
	}
	tenantID, ok := s.scopedTenant(w, r)
	if !ok || tenantID == nil {
		if ok {
			writeError(w, http.StatusBadRequest, 40002, "account context is required", requestID(r))
		}
		return nil, false
	}
	return tenantID, true
}

func (s *Service) loadAccountSettings(ctx context.Context, tenantID uuid.UUID) (accountSiteSettings, bool, error) {
	var encryptedEmail, encryptedHCaptcha, encryptedOIDC string
	err := s.db.QueryRow(ctx, `SELECT email_config_ciphertext,hcaptcha_config_ciphertext,oidc_config_ciphertext FROM account_settings WHERE tenant_id=$1`, tenantID).Scan(&encryptedEmail, &encryptedHCaptcha, &encryptedOIDC)
	if isNoRows(err) {
		return accountSiteSettings{}, false, nil
	}
	if err != nil {
		return accountSiteSettings{}, false, err
	}
	settings := accountSiteSettings{}
	if err = s.decryptConfig(encryptedEmail, &settings.Email); err != nil {
		return settings, true, err
	}
	if err = s.decryptConfig(encryptedHCaptcha, &settings.HCaptcha); err != nil {
		return settings, true, err
	}
	if err = s.decryptConfig(encryptedOIDC, &settings.OIDC); err != nil {
		return settings, true, err
	}
	return settings, true, nil
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
		"hcaptcha": map[string]any{"site_key": settings.HCaptcha.SiteKey, "secret_key_configured": settings.HCaptcha.SecretKey != ""},
		"oidc":     map[string]any{"issuer_url": settings.OIDC.IssuerURL, "client_id": settings.OIDC.ClientID, "redirect_url": settings.OIDC.RedirectURL, "client_secret_configured": settings.OIDC.ClientSecret != ""},
	}
}

func (s *Service) dashboard(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.scopedTenant(w, r)
	if !ok {
		return
	}
	var total, pending, paid, refunded int64
	var volume int64
	query := `SELECT COUNT(*),COALESCE(SUM(CASE WHEN status='pending' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN status='paid' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN status='refunded' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN status='paid' THEN amount_minor ELSE 0 END),0) FROM bills`
	args := []any{}
	if tenantID != nil {
		query += " WHERE tenant_id=$1"
		args = append(args, *tenantID)
	}
	err := s.db.QueryRow(r.Context(), query, args...).Scan(&total, &pending, &paid, &refunded, &volume)
	if err != nil {
		writeError(w, 500, 50001, "cannot load dashboard", requestID(r))
		return
	}
	writeJSON(w, 200, map[string]any{"total_bills": total, "pending_bills": pending, "paid_bills": paid, "refunded_bills": refunded, "paid_volume": moneyString(volume)})
}
func (s *Service) scopedTenant(w http.ResponseWriter, r *http.Request) (*uuid.UUID, bool) {
	p := currentPrincipal(r)
	if p.Role != "platform_admin" {
		return p.TenantID, true
	}
	raw := r.Header.Get("X-Tenant-ID")
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, 400, 40002, "invalid X-Tenant-ID", requestID(r))
		return nil, false
	}
	return &id, true
}

func (s *Service) listTenants(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if p.Role != "platform_admin" {
		writeError(w, 403, 40301, "platform administrator required", requestID(r))
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT id,name,merchant_no,status,created_at FROM tenants ORDER BY created_at DESC`)
	if err != nil {
		writeError(w, 500, 50001, "cannot load tenants", requestID(r))
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var name, no, status string
		var created time.Time
		if rows.Scan(&id, &name, &no, &status, &created) == nil {
			items = append(items, map[string]any{"id": id, "name": name, "merchant_no": no, "status": status, "created_at": created})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Service) createTenant(w http.ResponseWriter, r *http.Request) {
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
	_, err = s.db.Exec(r.Context(), `INSERT INTO tenants (id,name,merchant_no,api_secret_ciphertext,callback_secret_ciphertext) VALUES ($1,$2,$3,$4,$5)`, id, input.Name, input.MerchantNo, api, callback)
	if err != nil {
		writeError(w, 409, 40006, "merchant number already exists", requestID(r))
		return
	}
	s.audit(r.Context(), &id, &p.UserID, "tenant.create", "tenant", id.String(), requestID(r), map[string]string{"name": input.Name})
	writeJSON(w, 201, map[string]any{"id": id, "name": input.Name, "merchant_no": input.MerchantNo})
}
func (s *Service) patchTenant(w http.ResponseWriter, r *http.Request, idText string) {
	p := currentPrincipal(r)
	if p.Role != "platform_admin" {
		writeError(w, 403, 40301, "platform administrator required", requestID(r))
		return
	}
	id, err := uuid.Parse(idText)
	if err != nil {
		writeError(w, 400, 40002, "invalid tenant id", requestID(r))
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
		writeError(w, 400, 40002, "invalid tenant status", requestID(r))
		return
	}
	if input.Name != nil {
		_, _ = s.db.Exec(r.Context(), `UPDATE tenants SET name=$2,updated_at=NOW() WHERE id=$1`, id, *input.Name)
	}
	if input.Status != nil {
		_, _ = s.db.Exec(r.Context(), `UPDATE tenants SET status=$2,updated_at=NOW() WHERE id=$1`, id, *input.Status)
	}
	if input.APISecret != nil {
		value, _ := s.encrypt(*input.APISecret)
		_, _ = s.db.Exec(r.Context(), `UPDATE tenants SET api_secret_ciphertext=$2,updated_at=NOW() WHERE id=$1`, id, value)
	}
	if input.CallbackSecret != nil {
		value, _ := s.encrypt(*input.CallbackSecret)
		_, _ = s.db.Exec(r.Context(), `UPDATE tenants SET callback_secret_ciphertext=$2,updated_at=NOW() WHERE id=$1`, id, value)
	}
	s.audit(r.Context(), &id, &p.UserID, "tenant.update", "tenant", id.String(), requestID(r), map[string]any{"secret_rotated": input.APISecret != nil || input.CallbackSecret != nil})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.scopedTenant(w, r)
	if !ok {
		return
	}
	query := `SELECT id,tenant_id,email,display_name,role,is_active,created_at FROM users`
	args := []any{}
	if tenantID != nil {
		query += " WHERE tenant_id=$1"
		args = append(args, *tenantID)
	}
	query += " ORDER BY created_at DESC"
	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, 50001, "cannot load users", requestID(r))
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var tid *uuid.UUID
		var email, name, role string
		var active bool
		var created time.Time
		if rows.Scan(&id, &tid, &email, &name, &role, &active, &created) == nil {
			items = append(items, map[string]any{"id": id, "tenant_id": tid, "email": email, "display_name": name, "role": role, "is_active": active, "created_at": created})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Service) createUser(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if p.Role != "platform_admin" && p.Role != "tenant_admin" {
		writeError(w, 403, 40301, "tenant administrator required", requestID(r))
		return
	}
	target, ok := s.scopedTenant(w, r)
	if !ok {
		return
	}
	var input struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		TenantID    string `json:"tenant_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if p.Role == "platform_admin" && input.TenantID != "" {
		parsed, err := uuid.Parse(input.TenantID)
		if err != nil {
			writeError(w, 400, 40002, "invalid tenant_id", requestID(r))
			return
		}
		target = &parsed
	}
	if target == nil || input.Email == "" || len(input.Password) < 10 || input.DisplayName == "" {
		writeError(w, 400, 40002, "tenant_id, email, display_name and a 10-character password are required", requestID(r))
		return
	}
	if input.Role == "" {
		input.Role = "tenant_operator"
	}
	if input.Role != "tenant_admin" && input.Role != "tenant_operator" && input.Role != "tenant_viewer" {
		writeError(w, 400, 40002, "invalid user role", requestID(r))
		return
	}
	if p.Role != "platform_admin" && input.Role == "tenant_admin" {
		writeError(w, 403, 40301, "only platform administrator may create tenant administrators", requestID(r))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, 500, 50001, "cannot secure password", requestID(r))
		return
	}
	id := uuid.New()
	_, err = s.db.Exec(r.Context(), `INSERT INTO users (id,tenant_id,email,password_hash,display_name,role) VALUES ($1,$2,$3,$4,$5,$6)`, id, target, strings.ToLower(input.Email), string(hash), input.DisplayName, input.Role)
	if err != nil {
		writeError(w, 409, 40006, "email already exists", requestID(r))
		return
	}
	s.audit(r.Context(), target, &p.UserID, "user.create", "user", id.String(), requestID(r), map[string]string{"email": input.Email, "role": input.Role})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Service) listChannels(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.scopedTenant(w, r)
	if !ok {
		return
	}
	if tenantID == nil {
		writeError(w, 400, 40002, "select a tenant through X-Tenant-ID", requestID(r))
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT id,provider,display_name,enabled,config_ciphertext,webhook_token,updated_at FROM payment_channels WHERE tenant_id=$1 ORDER BY provider`, tenantID)
	if err != nil {
		writeError(w, 500, 50001, "cannot load channels", requestID(r))
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var provider, name, ciphertext, token string
		var enabled bool
		var updated time.Time
		if rows.Scan(&id, &provider, &name, &enabled, &ciphertext, &token, &updated) == nil {
			items = append(items, map[string]any{"id": id, "provider": provider, "display_name": name, "enabled": enabled, "configured": ciphertext != "", "webhook_url": fmt.Sprintf("%s/api/v1/webhooks/%s/%s", s.baseURL, provider, token), "updated_at": updated})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Service) createChannel(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if !canManagePayments(p) {
		writeError(w, http.StatusForbidden, 40301, "payment operator role required", requestID(r))
		return
	}
	tenantID, ok := s.scopedTenant(w, r)
	if !ok || tenantID == nil {
		if ok {
			writeError(w, http.StatusBadRequest, 40002, "select a tenant through X-Tenant-ID", requestID(r))
		}
		return
	}
	var input struct {
		Provider    string `json:"provider"`
		DisplayName string `json:"display_name"`
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
	channelID := uuid.New()
	_, err := s.db.Exec(r.Context(), `INSERT INTO payment_channels (id,tenant_id,provider,display_name,webhook_token) VALUES ($1,$2,$3,$4,$5)`, channelID, *tenantID, input.Provider, input.DisplayName, randomToken(24))
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, 40006, "this payment provider has already been added", requestID(r))
			return
		}
		writeError(w, http.StatusInternalServerError, 50001, "cannot create payment channel", requestID(r))
		return
	}
	s.audit(r.Context(), tenantID, &p.UserID, "channel.create", "payment_channel", channelID.String(), requestID(r), map[string]string{"provider": input.Provider})
	writeJSON(w, http.StatusCreated, map[string]any{"id": channelID, "provider": input.Provider, "display_name": input.DisplayName})
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
		Enabled     *bool           `json:"enabled"`
		Config      json.RawMessage `json:"config"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var tenantID uuid.UUID
	var provider string
	err = s.db.QueryRow(r.Context(), `SELECT tenant_id,provider FROM payment_channels WHERE id=$1`, channelID).Scan(&tenantID, &provider)
	if err != nil {
		writeError(w, 404, 40401, "channel not found", requestID(r))
		return
	}
	if !s.mayAccessTenant(p, tenantID) {
		writeError(w, 403, 40301, "tenant access denied", requestID(r))
		return
	}
	if input.DisplayName != nil {
		_, _ = s.db.Exec(r.Context(), `UPDATE payment_channels SET display_name=$2,updated_at=NOW() WHERE id=$1`, channelID, *input.DisplayName)
	}
	if input.Config != nil {
		var config providerConfig
		if err = json.Unmarshal(input.Config, &config); err != nil {
			writeError(w, 400, 40002, "invalid provider config", requestID(r))
			return
		}
		if err = validateProviderConfig(provider, config); err != nil {
			writeError(w, 400, 40002, err.Error(), requestID(r))
			return
		}
		encrypted, err := s.encrypt(string(input.Config))
		if err != nil {
			writeError(w, 500, 50001, "cannot secure channel config", requestID(r))
			return
		}
		_, _ = s.db.Exec(r.Context(), `UPDATE payment_channels SET config_ciphertext=$2,updated_at=NOW() WHERE id=$1`, channelID, encrypted)
	}
	if input.Enabled != nil {
		if *input.Enabled {
			var ciphertext string
			_ = s.db.QueryRow(r.Context(), `SELECT config_ciphertext FROM payment_channels WHERE id=$1`, channelID).Scan(&ciphertext)
			if ciphertext == "" {
				writeError(w, 400, 40002, "configure channel credentials before enabling", requestID(r))
				return
			}
		}
		_, _ = s.db.Exec(r.Context(), `UPDATE payment_channels SET enabled=$2,updated_at=NOW() WHERE id=$1`, channelID, *input.Enabled)
	}
	s.audit(r.Context(), &tenantID, &p.UserID, "channel.update", "payment_channel", channelID.String(), requestID(r), map[string]any{"provider": provider, "config_updated": input.Config != nil, "enabled": input.Enabled})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) listBills(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.scopedTenant(w, r)
	if !ok {
		return
	}
	args := []any{}
	query := `SELECT id,tenant_id,channel_id,platform_order_no,merchant_order_no,subject,description,amount_minor,currency,provider,scene,status,COALESCE(provider_transaction_id,''),notify_url,COALESCE(return_url,''),COALESCE(metadata,''),expires_at,paid_at,closed_at,created_at,updated_at FROM bills WHERE 1=1`
	if tenantID != nil {
		args = append(args, *tenantID)
		query += " AND tenant_id=$1"
	}
	if status := r.URL.Query().Get("status"); status != "" {
		args = append(args, status)
		query += fmt.Sprintf(" AND status=$%d", len(args))
	}
	query += " ORDER BY created_at DESC LIMIT 100"
	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, 50001, "cannot load bills", requestID(r))
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var bill billRecord
		if scanBill(rows, &bill) == nil {
			items = append(items, billPublic(bill))
		}
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
	if !s.mayAccessTenant(currentPrincipal(r), bill.TenantID) {
		writeError(w, 403, 40301, "tenant access denied", requestID(r))
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
	if !s.mayAccessTenant(p, bill.TenantID) {
		writeError(w, 403, 40301, "tenant access denied", requestID(r))
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
	refund := refundRecord{ID: uuid.New(), TenantID: bill.TenantID, BillID: bill.ID, RefundOrderNo: input.RefundOrderNo, AmountMinor: amount, Reason: input.Reason, Status: "pending", CreatedAt: nowUTC()}
	_, err = s.db.Exec(r.Context(), `INSERT INTO refunds (id,tenant_id,bill_id,refund_order_no,amount_minor,reason) VALUES ($1,$2,$3,$4,$5,$6)`, refund.ID, refund.TenantID, refund.BillID, refund.RefundOrderNo, refund.AmountMinor, refund.Reason)
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
		_, _ = s.db.Exec(r.Context(), `UPDATE refunds SET status='failed',updated_at=NOW() WHERE id=$1`, refund.ID)
		writeError(w, 502, 50001, "refund channel request failed", requestID(r))
		return
	}
	data, _ := json.Marshal(payload)
	_, _ = s.db.Exec(r.Context(), `UPDATE refunds SET status='succeeded',provider_refund_id=$2,provider_payload=$3,updated_at=NOW() WHERE id=$1`, refund.ID, nullString(tradeID), data)
	_, _ = s.db.Exec(r.Context(), `UPDATE bills SET status='refunded',updated_at=NOW() WHERE id=$1`, bill.ID)
	s.audit(r.Context(), &bill.TenantID, &p.UserID, "bill.refund", "bill", bill.ID.String(), requestID(r), map[string]any{"refund_order_no": input.RefundOrderNo, "amount": input.Amount})
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
	if !s.mayAccessTenant(p, bill.TenantID) {
		writeError(w, 403, 40301, "tenant access denied", requestID(r))
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
	_, _ = s.db.Exec(r.Context(), `UPDATE bills SET status='closed',closed_at=NOW(),updated_at=NOW() WHERE id=$1 AND status='pending'`, bill.ID)
	s.audit(r.Context(), &bill.TenantID, &p.UserID, "bill.close", "bill", bill.ID.String(), requestID(r), map[string]any{})
	w.WriteHeader(http.StatusNoContent)
}
func (s *Service) listRefunds(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.scopedTenant(w, r)
	if !ok {
		return
	}
	query := `SELECT id,tenant_id,bill_id,refund_order_no,amount_minor,reason,status,COALESCE(provider_refund_id,''),created_at FROM refunds`
	args := []any{}
	if tenantID != nil {
		query += " WHERE tenant_id=$1"
		args = append(args, *tenantID)
	}
	query += " ORDER BY created_at DESC LIMIT 100"
	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, 50001, "cannot load refunds", requestID(r))
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var item refundRecord
		if rows.Scan(&item.ID, &item.TenantID, &item.BillID, &item.RefundOrderNo, &item.AmountMinor, &item.Reason, &item.Status, &item.ProviderRefundID, &item.CreatedAt) == nil {
			items = append(items, map[string]any{"id": item.ID, "bill_id": item.BillID, "refund_order_no": item.RefundOrderNo, "amount": moneyString(item.AmountMinor), "reason": item.Reason, "status": item.Status, "provider_refund_id": item.ProviderRefundID, "created_at": item.CreatedAt})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *Service) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.scopedTenant(w, r)
	if !ok {
		return
	}
	query := `SELECT id,tenant_id,actor_user_id,action,target_type,target_id,request_id,detail,created_at FROM audit_logs`
	args := []any{}
	if tenantID != nil {
		query += " WHERE tenant_id=$1"
		args = append(args, *tenantID)
	}
	query += " ORDER BY created_at DESC LIMIT 100"
	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, 50001, "cannot load audit logs", requestID(r))
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var tid, uid *uuid.UUID
		var action, targetType, targetID, rid string
		var detail []byte
		var created time.Time
		if rows.Scan(&id, &tid, &uid, &action, &targetType, &targetID, &rid, &detail, &created) == nil {
			var parsed any
			_ = json.Unmarshal(detail, &parsed)
			items = append(items, map[string]any{"id": id, "tenant_id": tid, "actor_user_id": uid, "action": action, "target_type": targetType, "target_id": targetID, "request_id": rid, "detail": parsed, "created_at": created})
		}
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
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO webhook_events (id,tenant_id,channel_id,provider,event_key,verified,payload,processed_at) VALUES ($1,$2,$3,$4,$5,true,$6,NOW())`, uuid.New(), channel.TenantID, channel.ID, channel.Provider, result.EventKey, raw)
	if err != nil {
		if isUniqueViolation(err) {
			return tx.Commit(ctx)
		}
		return err
	}
	affected, err := tag.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return tx.Commit(ctx)
	}
	if !result.Paid {
		return tx.Commit(ctx)
	}
	var bill billRecord
	err = tx.QueryRow(ctx, `SELECT id,tenant_id,channel_id,platform_order_no,merchant_order_no,subject,description,amount_minor,currency,provider,scene,status,COALESCE(provider_transaction_id,''),notify_url,COALESCE(return_url,''),COALESCE(metadata,''),expires_at,paid_at,closed_at,created_at,updated_at FROM bills WHERE tenant_id=$1 AND merchant_order_no=$2 FOR UPDATE`, channel.TenantID, result.MerchantOrderNo).Scan(&bill.ID, &bill.TenantID, &bill.ChannelID, &bill.PlatformOrderNo, &bill.MerchantOrderNo, &bill.Subject, &bill.Description, &bill.AmountMinor, &bill.Currency, &bill.Provider, &bill.Scene, &bill.Status, &bill.ProviderTransactionID, &bill.NotifyURL, &bill.ReturnURL, &bill.Metadata, &bill.ExpiresAt, &bill.PaidAt, &bill.ClosedAt, &bill.CreatedAt, &bill.UpdatedAt)
	if err != nil {
		return err
	}
	if bill.Status == "pending" {
		_, err = tx.Exec(ctx, `UPDATE bills SET status='paid',provider_transaction_id=$2,provider_payload=$3,paid_at=NOW(),updated_at=NOW() WHERE id=$1`, bill.ID, nullString(result.ProviderTradeID), raw)
		if err != nil {
			return err
		}
		bill.Status = "paid"
		bill.ProviderTransactionID = result.ProviderTradeID
		paid := nowUTC()
		bill.PaidAt = &paid
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if bill.Status == "paid" {
		go s.notifyMerchant(context.Background(), bill)
	}
	return nil
}
func (s *Service) notifyMerchant(ctx context.Context, bill billRecord) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var encrypted string
	err := s.db.QueryRow(ctx, `SELECT callback_secret_ciphertext FROM tenants WHERE id=$1`, bill.TenantID).Scan(&encrypted)
	if err != nil {
		return
	}
	secret, err := s.decrypt(encrypted)
	if err != nil {
		return
	}
	values := url.Values{"pid": {s.merchantNo(ctx, bill.TenantID)}, "type": {providerOPSCode(bill.Provider)}, "out_trade_no": {bill.MerchantOrderNo}, "trade_no": {bill.ProviderTransactionID}, "name": {bill.Subject}, "money": {moneyString(bill.AmountMinor)}, "trade_status": {"TRADE_SUCCESS"}, "param": {bill.Metadata}, "sign_type": {"HMAC-SHA256"}}
	values.Set("sign", signHMAC(canonicalValues(values), secret))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bill.NotifyURL, strings.NewReader(values.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err == nil && resp != nil {
		_ = resp.Body.Close()
	}
}
func (s *Service) merchantNo(ctx context.Context, tenantID uuid.UUID) string {
	var value string
	_ = s.db.QueryRow(ctx, `SELECT merchant_no FROM tenants WHERE id=$1`, tenantID).Scan(&value)
	return value
}

func (s *Service) tenantByMerchantNo(ctx context.Context, no string) (tenantRecord, error) {
	var encryptedAPI, encryptedCallback string
	var tenant tenantRecord
	err := s.db.QueryRow(ctx, `SELECT id,name,merchant_no,status,api_secret_ciphertext,callback_secret_ciphertext,created_at FROM tenants WHERE merchant_no=$1 AND status='active'`, no).Scan(&tenant.ID, &tenant.Name, &tenant.MerchantNo, &tenant.Status, &encryptedAPI, &encryptedCallback, &tenant.CreatedAt)
	if err != nil {
		return tenant, err
	}
	tenant.APISecret, err = s.decrypt(encryptedAPI)
	if err != nil {
		return tenant, err
	}
	tenant.CallbackSecret, err = s.decrypt(encryptedCallback)
	return tenant, err
}
func (s *Service) channelByID(ctx context.Context, id uuid.UUID) (channelRecord, error) {
	var c channelRecord
	var encrypted string
	err := s.db.QueryRow(ctx, `SELECT id,tenant_id,provider,display_name,enabled,config_ciphertext,webhook_token FROM payment_channels WHERE id=$1`, id).Scan(&c.ID, &c.TenantID, &c.Provider, &c.DisplayName, &c.Enabled, &encrypted, &c.WebhookToken)
	if err != nil {
		return c, err
	}
	plain, err := s.decrypt(encrypted)
	if err != nil {
		return c, err
	}
	if plain != "" {
		err = json.Unmarshal([]byte(plain), &c.Config)
	}
	return c, err
}
func (s *Service) billByID(ctx context.Context, id uuid.UUID) (billRecord, error) {
	var bill billRecord
	err := s.db.QueryRow(ctx, `SELECT id,tenant_id,channel_id,platform_order_no,merchant_order_no,subject,description,amount_minor,currency,provider,scene,status,COALESCE(provider_transaction_id,''),notify_url,COALESCE(return_url,''),COALESCE(metadata,''),expires_at,paid_at,closed_at,created_at,updated_at FROM bills WHERE id=$1`, id).Scan(&bill.ID, &bill.TenantID, &bill.ChannelID, &bill.PlatformOrderNo, &bill.MerchantOrderNo, &bill.Subject, &bill.Description, &bill.AmountMinor, &bill.Currency, &bill.Provider, &bill.Scene, &bill.Status, &bill.ProviderTransactionID, &bill.NotifyURL, &bill.ReturnURL, &bill.Metadata, &bill.ExpiresAt, &bill.PaidAt, &bill.ClosedAt, &bill.CreatedAt, &bill.UpdatedAt)
	return bill, err
}
func (s *Service) billByMerchantOrder(ctx context.Context, tenantID uuid.UUID, orderNo string) (billRecord, error) {
	var bill billRecord
	err := s.db.QueryRow(ctx, `SELECT id,tenant_id,channel_id,platform_order_no,merchant_order_no,subject,description,amount_minor,currency,provider,scene,status,COALESCE(provider_transaction_id,''),notify_url,COALESCE(return_url,''),COALESCE(metadata,''),expires_at,paid_at,closed_at,created_at,updated_at FROM bills WHERE tenant_id=$1 AND merchant_order_no=$2`, tenantID, orderNo).Scan(&bill.ID, &bill.TenantID, &bill.ChannelID, &bill.PlatformOrderNo, &bill.MerchantOrderNo, &bill.Subject, &bill.Description, &bill.AmountMinor, &bill.Currency, &bill.Provider, &bill.Scene, &bill.Status, &bill.ProviderTransactionID, &bill.NotifyURL, &bill.ReturnURL, &bill.Metadata, &bill.ExpiresAt, &bill.PaidAt, &bill.ClosedAt, &bill.CreatedAt, &bill.UpdatedAt)
	return bill, err
}
func scanBill(row pgx.Row, b *billRecord) error {
	return row.Scan(&b.ID, &b.TenantID, &b.ChannelID, &b.PlatformOrderNo, &b.MerchantOrderNo, &b.Subject, &b.Description, &b.AmountMinor, &b.Currency, &b.Provider, &b.Scene, &b.Status, &b.ProviderTransactionID, &b.NotifyURL, &b.ReturnURL, &b.Metadata, &b.ExpiresAt, &b.PaidAt, &b.ClosedAt, &b.CreatedAt, &b.UpdatedAt)
}
func billPublic(b billRecord) map[string]any {
	return map[string]any{"id": b.ID, "platform_order_no": b.PlatformOrderNo, "merchant_order_no": b.MerchantOrderNo, "subject": b.Subject, "description": b.Description, "amount": moneyString(b.AmountMinor), "currency": b.Currency, "provider": b.Provider, "scene": b.Scene, "status": b.Status, "provider_transaction_id": b.ProviderTransactionID, "notify_url": b.NotifyURL, "return_url": b.ReturnURL, "metadata": b.Metadata, "expires_at": b.ExpiresAt, "paid_at": b.PaidAt, "closed_at": b.ClosedAt, "created_at": b.CreatedAt, "updated_at": b.UpdatedAt}
}
func (s *Service) mayAccessTenant(p principal, tenantID uuid.UUID) bool {
	return p.Role == "platform_admin" || (p.TenantID != nil && *p.TenantID == tenantID)
}
func canManagePayments(p principal) bool {
	return p.Role == "platform_admin" || p.Role == "tenant_admin" || p.Role == "tenant_operator"
}
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}
func (s *Service) validCallbackURL(raw string) bool {
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
	return parsed.Scheme == "https" || parsed.Scheme == "http"
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
	return paymentInput{MerchantID: first(values, "merchant_id", "pid", "mch_id"), PaymentMethod: first(values, "payment_method", "type"), MerchantOrderNo: first(values, "merchant_order_no", "out_trade_no"), Subject: first(values, "subject", "name"), Description: values.Get("description"), Amount: first(values, "amount", "money"), NotifyURL: values.Get("notify_url"), ReturnURL: values.Get("return_url"), Metadata: first(values, "metadata", "param"), Scene: first(values, "scene", "device"), SignType: values.Get("sign_type"), Sign: values.Get("sign")}
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
	if input.SignType == "" {
		input.SignType = "HMAC-SHA256"
	}
	values := url.Values{"pid": {input.MerchantID}, "type": {providerOPSCode(input.PaymentMethod)}, "out_trade_no": {input.MerchantOrderNo}, "name": {input.Subject}, "money": {input.Amount}, "notify_url": {input.NotifyURL}, "return_url": {input.ReturnURL}, "param": {input.Metadata}}
	canonical := canonicalValues(values)
	var expected string
	switch strings.ToUpper(input.SignType) {
	case "MD5":
		sum := md5.Sum([]byte(canonical + secret))
		expected = hex.EncodeToString(sum[:])
	case "HMAC-SHA256":
		expected = signHMAC(canonical, secret)
	default:
		return errors.New("unsupported sign type")
	}
	if !hmac.Equal([]byte(strings.ToLower(expected)), []byte(strings.ToLower(input.Sign))) {
		return errors.New("signature mismatch")
	}
	return nil
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
		if config.Alipay == nil || config.Alipay.AppID == "" || config.Alipay.AppPrivateKeyPEM == "" || config.Alipay.AlipayPublicKeyPEM == "" {
			return errors.New("支付宝配置需要 app_id、app_private_key_pem、alipay_public_key_pem")
		}
		return nil
	}
	if provider == "wechat" {
		if config.Wechat == nil || config.Wechat.MchID == "" || config.Wechat.AppID == "" || config.Wechat.MerchantSerialNo == "" || config.Wechat.MerchantPrivateKeyPEM == "" || config.Wechat.APIv3Key == "" || config.Wechat.PlatformPublicKeyPEM == "" {
			return errors.New("微信支付配置需要 mch_id、app_id、merchant_serial_no、merchant_private_key_pem、api_v3_key、platform_public_key_pem")
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
func writePublicError(w http.ResponseWriter, r *http.Request, err error) {
	var ce clientError
	if errors.As(err, &ce) {
		writeError(w, 400, ce.Code, ce.Message, requestID(r))
		return
	}
	writeError(w, 502, 50001, "payment service unavailable", requestID(r))
}
func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func _unused(_ context.Context, _ strconv.NumError) {}
