package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("record not found")

// Store wraps the GORM handle. Repositories are methods on Store so that
// services only need one dependency.
type Store struct {
	DB   *gorm.DB
	path string
}

// Open opens (and creates) the SQLite database at path and applies the SQL
// migrations. SQLite runs with WAL, foreign keys and a busy timeout.
func Open(path string, debug bool) (*Store, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	logMode := logger.Silent
	if debug {
		logMode = logger.Info
	}
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:                 logger.Default.LogMode(logMode),
		SkipDefaultTransaction: true,
		NowFunc:                func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	// The pure-Go SQLite driver is happiest with a single writer. WAL keeps
	// reads concurrent for short queries.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{DB: db, path: path}
	if err := s.Migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	sqlDB, err := s.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

func wrapErr(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

// tx runs fn inside a transaction.
func (s *Store) tx(fn func(tx *gorm.DB) error) error {
	return s.DB.Transaction(fn)
}
