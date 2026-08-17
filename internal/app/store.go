package app

import (
	"context"
	"fmt"
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

// DB is provided for model-based GORM repositories. New application code must
// prefer this over raw SQL helpers.
func (s *Store) DB() *gorm.DB { return s.db }

func (s *Store) ConfigurePool(maxOpen int) error {
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxOpen / 2)
	return nil
}
