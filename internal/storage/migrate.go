package storage

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/proxygate/proxygate/migrations"
	"gorm.io/gorm"
)

// Migrate applies embedded SQL migrations that have not run yet. Each file is
// applied in a transaction and recorded in schema_migrations.
func (s *Store) Migrate() error {
	if err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL
	)`).Error; err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var count int64
		if err := s.DB.Raw("SELECT count(*) FROM schema_migrations WHERE version = ?", name).Scan(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		raw, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return err
		}
		stmts := splitStatements(string(raw))
		if err := s.DB.Transaction(func(tx *gorm.DB) error {
			for _, stmt := range stmts {
				if err := tx.Exec(stmt).Error; err != nil {
					return fmt.Errorf("migration %s: %w", name, err)
				}
			}
			return tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES(?, datetime('now'))", name).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

// splitStatements splits a SQL script on semicolons while respecting single
// quotes, double quotes and comments. Migrations never contain triggers.
func splitStatements(script string) []string {
	var out []string
	var b strings.Builder
	var inSingle, inDouble, inLine, inBlock bool
	runes := []rune(script)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		var next rune
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				b.WriteRune(c)
			}
			continue
		case inBlock:
			if c == '*' && next == '/' {
				inBlock = false
				i++
			}
			continue
		case inSingle:
			b.WriteRune(c)
			if c == '\'' {
				if next == '\'' {
					b.WriteRune(next)
					i++
				} else {
					inSingle = false
				}
			}
			continue
		case inDouble:
			b.WriteRune(c)
			if c == '"' {
				inDouble = false
			}
			continue
		}
		switch {
		case c == '-' && next == '-':
			inLine = true
			i++
		case c == '/' && next == '*':
			inBlock = true
			i++
		case c == '\'':
			inSingle = true
			b.WriteRune(c)
		case c == '"':
			inDouble = true
			b.WriteRune(c)
		case c == ';':
			if stmt := strings.TrimSpace(b.String()); stmt != "" {
				out = append(out, stmt)
			}
			b.Reset()
		default:
			b.WriteRune(c)
		}
	}
	if stmt := strings.TrimSpace(b.String()); stmt != "" {
		out = append(out, stmt)
	}
	return out
}
