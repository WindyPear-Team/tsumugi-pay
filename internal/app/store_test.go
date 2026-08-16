package app

import (
	"context"
	"io"
	"log/slog"
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
	if users != 2 || channels != 2 {
		t.Fatalf("unexpected demo data: users=%d channels=%d", users, channels)
	}
}

func TestNormalizeSQL(t *testing.T) {
	query := normalizeSQL("mysql", `UPDATE bills SET updated_at=NOW() WHERE id=$1::uuid`)
	if query != "UPDATE bills SET updated_at=CURRENT_TIMESTAMP WHERE id=?" {
		t.Fatalf("unexpected normalized query: %s", query)
	}
}
