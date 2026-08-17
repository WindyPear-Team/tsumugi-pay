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

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
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
		"endpoints":       map[string]any{"submit": base + "/submit.php", "mapi": base + "/mapi.php", "api": base + "/api.php", "query": base + "/api.php?act=order"},
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
		return paymentResponse{}, err
	}
	if !channelModel.Enabled {
		return paymentResponse{}, clientError{40003, "payment channel is disabled"}
	}
	plain, err := s.decrypt(channelModel.ConfigCiphertext)
	if err != nil {
		return paymentResponse{}, err
	}
	channel := channelRecord{ID: channelModel.ID, AccountID: account.ID, Provider: input.PaymentMethod, Enabled: channelModel.Enabled, DisplayName: channelModel.DisplayName, WebhookToken: channelModel.WebhookToken}
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
	bill := billRecord{ID: uuid.New(), AccountID: account.ID, ChannelID: channelModel.ID, PlatformOrderNo: "TP" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))[:24], MerchantOrderNo: input.MerchantOrderNo, Subject: input.Subject, Description: input.Description, AmountMinor: amount, Currency: "CNY", Provider: input.PaymentMethod, Scene: scene, Status: "pending", NotifyURL: input.NotifyURL, ReturnURL: input.ReturnURL, Metadata: input.Metadata, ExpiresAt: &expires, CreatedAt: nowUTC(), UpdatedAt: nowUTC()}
	err = s.db.DB().WithContext(ctx).Create(bill.toModel()).Error
	if err != nil {
		if strings.Contains(err.Error(), "bills_account_id_merchant_order_no_key") {
			return paymentResponse{}, clientError{40006, "duplicate merchant order number"}
		}
		return paymentResponse{}, err
	}
	result, err := s.createPayment(ctx, channel, bill)
	if err != nil {
		_ = s.db.DB().WithContext(ctx).Model(&Bill{}).Where("id = ?", bill.ID).Update("status", "failed").Error
		return paymentResponse{}, fmt.Errorf("payment channel request failed: %w", err)
	}
	payload, _ := json.Marshal(result.Raw)
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
	var user User
	err := s.db.DB().WithContext(r.Context()).Where("email = ?", strings.ToLower(strings.TrimSpace(input.Email))).Take(&user).Error
	if err != nil || !user.IsActive || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)) != nil {
		writeError(w, 401, 40103, "invalid email or password", requestID(r))
		return
	}
	claims := jwtClaims{Role: user.Role, Email: strings.ToLower(strings.TrimSpace(input.Email)), RegisteredClaims: jwt.RegisteredClaims{Subject: user.ID.String(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())}}
	if user.AccountID != nil {
		claims.AccountID = user.AccountID.String()
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.jwtSecret)
	if err != nil {
		writeError(w, 500, 50001, "cannot issue access token", requestID(r))
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": signed, "token_type": "Bearer", "expires_in": 28800, "user": map[string]any{"id": user.ID, "email": claims.Email, "display_name": user.DisplayName, "role": user.Role, "account_id": user.AccountID}})
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
		return tx.Create(&User{ID: adminID, AccountID: &accountID, Email: input.Email, PasswordHash: string(hash), DisplayName: input.DisplayName, Role: "user", IsActive: true}).Error
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
	writeJSON(w, http.StatusCreated, map[string]any{"message": "initial user created", "user": map[string]any{"id": adminID, "email": input.Email, "display_name": input.DisplayName, "role": "user"}, "account": map[string]any{"id": accountID, "name": input.AccountName, "merchant_no": input.MerchantNo}, "credentials": map[string]string{"api_secret": apiSecret, "callback_secret": callbackSecret}})
}

func (s *Service) admin(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/admin")
	switch {
	case r.Method == "GET" && path == "/me":
		s.adminMe(w, r)
	case r.Method == "GET" && path == "/dashboard":
		s.dashboard(w, r)
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
	case strings.HasPrefix(path, "/bills/") && strings.HasSuffix(path, "/reconcile") && r.Method == "POST":
		s.reconcileBill(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/bills/"), "/reconcile"))
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
	response := map[string]any{"id": p.UserID, "email": p.Email, "role": p.Role, "account_id": p.AccountID}
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
	writeJSON(w, http.StatusOK, siteSettingsPublic(settings))
}

func (s *Service) patchSiteSettings(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.siteSettingsAccount(w, r)
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
	s.audit(r.Context(), accountID, &p.UserID, "site_settings.update", "site_settings", accountID.String(), requestID(r), map[string]bool{"email": input.Email != nil, "hcaptcha": input.HCaptcha != nil, "oidc": input.OIDC != nil})
	writeJSON(w, http.StatusOK, siteSettingsPublic(settings))
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
	if p.Role != "user" {
		writeError(w, http.StatusForbidden, 40301, "user account required", requestID(r))
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
		"oidc":     map[string]any{"enabled": settings.OIDC.Enabled, "issuer_url": settings.OIDC.IssuerURL, "client_id": settings.OIDC.ClientID, "redirect_url": settings.OIDC.RedirectURL, "authorization_endpoint": settings.OIDC.AuthorizationEndpoint, "token_endpoint": settings.OIDC.TokenEndpoint, "jwks_uri": settings.OIDC.JWKSURI, "client_secret_configured": settings.OIDC.ClientSecret != ""},
	}
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
		items = append(items, map[string]any{"id": channel.ID, "provider": channel.Provider, "display_name": channel.DisplayName, "priority": channel.Priority, "weight": channel.Weight, "enabled": channel.Enabled, "configured": channel.ConfigCiphertext != "", "webhook_url": fmt.Sprintf("%s/api/v1/webhooks/%s/%s", s.baseURL, channel.Provider, channel.WebhookToken), "updated_at": channel.UpdatedAt})
	}
	writeJSON(w, 200, map[string]any{"items": items})
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
		if err = validateProviderConfig(channel.Provider, config); err != nil {
			writeError(w, 400, 40002, err.Error(), requestID(r))
			return
		}
		encrypted, err := s.encrypt(string(input.Config))
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
	if err := s.db.DB().WithContext(ctx).Select("callback_secret_ciphertext").Take(&account, "id = ?", bill.AccountID).Error; err != nil {
		return
	}
	secret, err := s.decrypt(account.CallbackSecretCiphertext)
	if err != nil {
		return
	}
	values := url.Values{"pid": {s.merchantNo(ctx, bill.AccountID)}, "type": {providerOPSCode(bill.Provider)}, "out_trade_no": {bill.MerchantOrderNo}, "trade_no": {bill.ProviderTransactionID}, "name": {bill.Subject}, "money": {moneyString(bill.AmountMinor)}, "trade_status": {"TRADE_SUCCESS"}, "param": {bill.Metadata}, "sign_type": {"HMAC-SHA256"}}
	values.Set("sign", signHMAC(canonicalValues(values), secret))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bill.NotifyURL, strings.NewReader(values.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err == nil && resp != nil {
		_ = resp.Body.Close()
	}
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
	return p.Role == "user" && p.AccountID != nil
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
