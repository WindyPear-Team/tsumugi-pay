package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Config struct {
	Database      *Store
	JWTSecret     string
	EncryptionKey []byte
	PublicBaseURL string
	Environment   string
	BootstrapDemo bool
	HTTPClient    *http.Client
	Logger        *slog.Logger
}

type Service struct {
	db            *Store
	jwtSecret     []byte
	cipher        cipher.AEAD
	baseURL       string
	environment   string
	bootstrapDemo bool
	httpClient    *http.Client
	logger        *slog.Logger
}

type principal struct {
	UserID    uuid.UUID
	AccountID *uuid.UUID
	Role      string
	Email     string
}

type jwtClaims struct {
	AccountID string `json:"account_id,omitempty"`
	Role      string `json:"role"`
	Email     string `json:"email"`
	jwt.RegisteredClaims
}

func New(cfg Config) (*Service, error) {
	block, err := aes.NewCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	return &Service{
		db: cfg.Database, jwtSecret: []byte(cfg.JWTSecret), cipher: gcm,
		baseURL: cfg.PublicBaseURL, environment: cfg.Environment,
		bootstrapDemo: cfg.BootstrapDemo, httpClient: cfg.HTTPClient, logger: cfg.Logger,
	}, nil
}

func (s *Service) Bootstrap(ctx context.Context) error {
	if !s.bootstrapDemo {
		return nil
	}
	var existing int64
	if err := s.db.DB().WithContext(ctx).Model(&User{}).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("ChangeMe123!"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	callbackSecret, err := s.encrypt("demo-callback-secret-change-before-production")
	if err != nil {
		return err
	}
	apiSecret, err := s.encrypt("demo-api-secret-change-before-production")
	if err != nil {
		return err
	}
	accountID := uuid.New()
	return s.db.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&Account{ID: accountID, Name: "演示租户", MerchantNo: "100000", APISecretCiphertext: apiSecret, CallbackSecretCiphertext: callbackSecret}).Error; err != nil {
			return err
		}
		if err := tx.Create(&User{ID: uuid.New(), Email: "admin@tsumugi.local", PasswordHash: string(passwordHash), DisplayName: "平台管理员", Role: "platform_admin", IsActive: true}).Error; err != nil {
			return err
		}
		return tx.Create(&User{ID: uuid.New(), AccountID: &accountID, Email: "merchant@tsumugi.local", PasswordHash: string(passwordHash), DisplayName: "演示用户", Role: "user", IsActive: true}).Error
	})
}

func (s *Service) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /.well-known/openpayment-configuation", s.discovery)
	mux.HandleFunc("GET /.well-known/openpayment-configuration", s.discovery)
	mux.HandleFunc("POST /submit.php", s.legacyCreate)
	mux.HandleFunc("POST /mapi.php", s.publicCreate)
	mux.HandleFunc("GET /api.php", s.legacyAPI)
	mux.HandleFunc("POST /api.php", s.legacyAPI)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("GET /api/v1/setup/status", s.setupStatus)
	mux.HandleFunc("POST /api/v1/setup/initialize", s.setupInitialize)
	mux.HandleFunc("POST /api/v1/webhooks/alipay/{token}", s.alipayWebhook)
	mux.HandleFunc("POST /api/v1/webhooks/wechat/{token}", s.wechatWebhook)
	mux.Handle("/api/v1/admin/", s.auth(http.HandlerFunc(s.admin)))
	return s.middleware(mux)
}

func (s *Service) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Account-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("unhandled panic", "request_id", requestID, "error", recovered)
				writeError(w, http.StatusInternalServerError, 50001, "internal server error", requestID)
			}
		}()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID)))
	})
}

type requestIDKey struct{}

func (s *Service) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if header == "" {
			writeError(w, http.StatusUnauthorized, 40101, "missing bearer token", requestID(r))
			return
		}
		claims := &jwtClaims{}
		token, err := jwt.ParseWithClaims(header, claims, func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, errors.New("unexpected signing method")
			}
			return s.jwtSecret, nil
		})
		if err != nil || !token.Valid {
			writeError(w, http.StatusUnauthorized, 40102, "invalid or expired access token", requestID(r))
			return
		}
		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			writeError(w, http.StatusUnauthorized, 40102, "invalid access token subject", requestID(r))
			return
		}
		var accountID *uuid.UUID
		if claims.AccountID != "" {
			parsed, err := uuid.Parse(claims.AccountID)
			if err != nil {
				writeError(w, http.StatusUnauthorized, 40102, "invalid access token account", requestID(r))
				return
			}
			accountID = &parsed
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{UserID: userID, AccountID: accountID, Role: claims.Role, Email: claims.Email})))
	})
}

type principalKey struct{}

func currentPrincipal(r *http.Request) principal {
	value, _ := r.Context().Value(principalKey{}).(principal)
	return value
}
func requestID(r *http.Request) string {
	value, _ := r.Context().Value(requestIDKey{}).(string)
	return value
}

func (s *Service) encrypt(plain string) (string, error) {
	nonce := make([]byte, s.cipher.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(append(nonce, s.cipher.Seal(nil, nonce, []byte(plain), nil)...)), nil
}
func (s *Service) decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	if len(payload) < s.cipher.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	plain, err := s.cipher.Open(nil, payload[:s.cipher.NonceSize()], payload[s.cipher.NonceSize():], nil)
	return string(plain), err
}
func randomToken(bytesLen int) string {
	b := make([]byte, bytesLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status, code int, message, rid string) {
	writeJSON(w, status, map[string]any{"code": code, "message": message, "request_id": rid})
}
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, 40002, "invalid JSON body", requestID(r))
		return false
	}
	return true
}
func moneyString(minor int64) string { return fmt.Sprintf("%d.%02d", minor/100, minor%100) }
func parseAmount(value string) (int64, error) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" {
		return 0, errors.New("invalid amount")
	}
	whole := int64(0)
	if _, err := fmt.Sscan(parts[0], &whole); err != nil || whole < 0 {
		return 0, errors.New("invalid amount")
	}
	cents := int64(0)
	if len(parts) == 2 {
		if len(parts[1]) > 2 {
			return 0, errors.New("invalid amount")
		}
		normalized := parts[1] + strings.Repeat("0", 2-len(parts[1]))
		if _, err := fmt.Sscan(normalized, &cents); err != nil {
			return 0, errors.New("invalid amount")
		}
	}
	if whole > 100000000 {
		return 0, errors.New("amount too large")
	}
	return whole*100 + cents, nil
}
func (s *Service) audit(ctx context.Context, accountID *uuid.UUID, actor *uuid.UUID, action, targetType, targetID, rid string, detail any) {
	bytes, _ := json.Marshal(detail)
	err := s.db.DB().WithContext(ctx).Create(&AuditLog{ID: uuid.New(), AccountID: accountID, ActorUserID: actor, Action: action, TargetType: targetType, TargetID: targetID, RequestID: rid, Detail: string(bytes)}).Error
	if err != nil {
		s.logger.Error("audit log failed", "error", err)
	}
}
func isNoRows(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
func nowUTC() time.Time       { return time.Now().UTC() }
