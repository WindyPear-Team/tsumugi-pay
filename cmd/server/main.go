package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/WindyPear-Team/tsumugi-pay/internal/app"
	"github.com/WindyPear-Team/tsumugi-pay/web"
	"github.com/joho/godotenv"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	// Local development commonly keeps credentials in .env. Load it without
	// replacing values explicitly supplied by the shell or deployment runtime.
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warn("could not load .env", "error", err)
	}
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	database, err := app.OpenDatabase(cfg.DatabaseDriver, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	store := app.NewStore(database)
	if err := store.ConfigurePool(12); err != nil {
		logger.Error("database pool configuration failed", "error", err)
		os.Exit(1)
	}
	if err := store.Ping(ctx); err != nil {
		logger.Error("database ping failed", "error", err)
		os.Exit(1)
	}
	if err := store.Migrate(ctx); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}

	service, err := app.New(app.Config{
		Database:      store,
		JWTSecret:     cfg.JWTSecret,
		EncryptionKey: cfg.EncryptionKey,
		PublicBaseURL: cfg.PublicBaseURL,
		Environment:   cfg.Environment,
		BootstrapDemo: cfg.BootstrapDemo,
		HTTPClient:    &http.Client{Timeout: 20 * time.Second},
		Logger:        logger,
	})
	if err != nil {
		logger.Error("service initialization failed", "error", err)
		os.Exit(1)
	}
	if err := service.Bootstrap(ctx); err != nil {
		logger.Error("bootstrap failed", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/", web.Handler(service.Routes()))
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		logger.Info("tsumugi pay is listening", "address", cfg.ListenAddr, "environment", cfg.Environment)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			cancel()
		}
	}()
	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}

type config struct {
	DatabaseDriver string
	DatabaseURL    string
	ListenAddr     string
	JWTSecret      string
	EncryptionKey  []byte
	PublicBaseURL  string
	Environment    string
	BootstrapDemo  bool
}

func loadConfig() (config, error) {
	cfg := config{
		DatabaseDriver: env("DATABASE_DRIVER", "postgres"),
		DatabaseURL:    env("DATABASE_URL", "postgres://tsumugi:tsumugi@localhost:5432/tsumugi_pay?sslmode=disable"),
		ListenAddr:     env("LISTEN_ADDR", ":8080"),
		JWTSecret:      env("JWT_SECRET", "change-this-development-jwt-secret"),
		PublicBaseURL:  strings.TrimRight(env("PUBLIC_BASE_URL", "http://localhost:8080"), "/"),
		Environment:    env("APP_ENV", "development"),
		BootstrapDemo:  env("BOOTSTRAP_DEMO", "true") == "true",
	}
	encodedKey := os.Getenv("APP_ENCRYPTION_KEY")
	if encodedKey == "" {
		if cfg.Environment == "production" {
			return cfg, errors.New("APP_ENCRYPTION_KEY is required in production")
		}
		sum := sha256.Sum256([]byte(cfg.JWTSecret + ":tsumugi-pay-development-key"))
		cfg.EncryptionKey = sum[:]
	} else {
		key, err := base64.StdEncoding.DecodeString(encodedKey)
		if err != nil || len(key) != 32 {
			return cfg, errors.New("APP_ENCRYPTION_KEY must be a base64 encoded 32-byte key")
		}
		cfg.EncryptionKey = key
	}
	if cfg.Environment == "production" && cfg.JWTSecret == "change-this-development-jwt-secret" {
		return cfg, errors.New("JWT_SECRET must be changed in production")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
