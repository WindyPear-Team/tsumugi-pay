package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Store 是所有持久化操作的统一入口。它保留现有的 PostgreSQL 迁移和查询语义，
// 同时将连接、上下文、事务与执行结果全部交由 GORM 管理。
type Store struct {
	db *gorm.DB
}

var postgresPlaceholder = regexp.MustCompile(`\$[0-9]+`)

// OpenDatabase opens a supported SQL database. Database URLs are kept in the
// standard driver format so deployments can switch stores without code changes.
func OpenDatabase(driver, dsn string) (*gorm.DB, error) {
	driver = strings.ToLower(strings.TrimSpace(driver))
	if driver == "" {
		switch {
		case strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://"):
			driver = "postgres"
		case strings.HasPrefix(dsn, "sqlite://"), strings.HasPrefix(dsn, "file:"), strings.HasSuffix(dsn, ".db"):
			driver = "sqlite"
		default:
			driver = "mysql"
		}
	}
	if driver == "sqlite" && strings.HasPrefix(dsn, "sqlite://") {
		dsn = strings.TrimPrefix(dsn, "sqlite://")
	}
	switch driver {
	case "postgres", "postgresql":
		return gorm.Open(postgres.Open(dsn), &gorm.Config{})
	case "mysql":
		return gorm.Open(mysql.Open(dsn), &gorm.Config{})
	case "sqlite", "sqlite3":
		return gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	default:
		return nil, fmt.Errorf("unsupported DATABASE_DRIVER %q (use postgres, mysql, or sqlite)", driver)
	}
}

// NewStore 用于将 GORM 实例注入业务层。
func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Ping(ctx context.Context) error {
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	return db.PingContext(ctx)
}

func (s *Store) ConfigurePool(maxOpen int) error {
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxOpen / 2)
	return nil
}

func (s *Store) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.WithContext(ctx).Raw(s.normalizeSQL(query), args...).Row()
}

func (s *Store) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.WithContext(ctx).Raw(s.normalizeSQL(query), args...).Rows()
}

func (s *Store) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result := s.db.WithContext(ctx).Exec(s.normalizeSQL(query), args...)
	if result.Error != nil {
		return nil, result.Error
	}
	return gormResult{rowsAffected: result.RowsAffected}, nil
}

func (s *Store) Begin(ctx context.Context) (*StoreTx, error) {
	transaction := s.db.WithContext(ctx).Begin()
	if transaction.Error != nil {
		return nil, transaction.Error
	}
	return &StoreTx{db: transaction, dialect: s.db.Dialector.Name()}, nil
}

// StoreTx 提供和 Store 一致的查询接口，避免业务操作绕开 GORM 事务。
type StoreTx struct {
	db      *gorm.DB
	dialect string
}

func (t *StoreTx) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return t.db.WithContext(ctx).Raw(normalizeSQL(t.dialect, query), args...).Row()
}

func (t *StoreTx) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result := t.db.WithContext(ctx).Exec(normalizeSQL(t.dialect, query), args...)
	if result.Error != nil {
		return nil, result.Error
	}
	return gormResult{rowsAffected: result.RowsAffected}, nil
}

func (t *StoreTx) Commit(ctx context.Context) error {
	return t.db.WithContext(ctx).Commit().Error
}

func (t *StoreTx) Rollback(ctx context.Context) {
	_ = t.db.WithContext(ctx).Rollback().Error
}

type gormResult struct {
	rowsAffected int64
}

func (r gormResult) LastInsertId() (int64, error) {
	return 0, errors.New("store does not expose LastInsertId; application IDs are UUIDs")
}

func (s *Store) normalizeSQL(query string) string { return normalizeSQL(s.db.Dialector.Name(), query) }

// The application uses PostgreSQL-style numbered placeholders in its SQL. This
// adapter keeps those query definitions portable for MySQL and SQLite.
func normalizeSQL(dialect, query string) string {
	query = strings.ReplaceAll(query, "NOW()", "CURRENT_TIMESTAMP")
	if dialect != "postgres" {
		query = postgresPlaceholder.ReplaceAllString(query, "?")
		query = strings.ReplaceAll(query, "::uuid", "")
		if dialect == "sqlite" {
			query = strings.ReplaceAll(query, " FOR UPDATE", "")
		}
	}
	return query
}

func (r gormResult) RowsAffected() (int64, error) {
	if r.rowsAffected < 0 {
		return 0, fmt.Errorf("invalid affected row count")
	}
	return r.rowsAffected, nil
}
