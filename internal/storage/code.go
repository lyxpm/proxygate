package storage

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"gorm.io/gorm"
)

const businessCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewBusinessCode returns a short, cryptographically random code with a
// module prefix. Codes are stable business identifiers, not database IDs.
func NewBusinessCode(prefix string) (string, error) {
	out := make([]byte, 6)
	max := big.NewInt(int64(len(businessCodeAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = businessCodeAlphabet[n.Int64()]
	}
	return prefix + "-" + string(out), nil
}

func (s *Site) BeforeCreate(tx *gorm.DB) error {
	if s.Code != "" {
		return nil
	}
	code, err := NewBusinessCode("SITE")
	if err != nil {
		return err
	}
	s.Code = code
	return nil
}

func (a *Application) BeforeCreate(tx *gorm.DB) error {
	if a.Code != "" {
		return nil
	}
	code, err := NewBusinessCode("APP")
	if err != nil {
		return err
	}
	a.Code = code
	return nil
}

func (c *Certificate) BeforeCreate(tx *gorm.DB) error {
	if c.Code != "" {
		return nil
	}
	code, err := NewBusinessCode("CERT")
	if err != nil {
		return err
	}
	c.Code = code
	return nil
}

// backfillBusinessCodes assigns codes to rows created before migration 0017.
func (s *Store) backfillBusinessCodes() error {
	var sites []Site
	if err := s.DB.Where("code IS NULL OR code = ''").Find(&sites).Error; err != nil {
		return err
	}
	for i := range sites {
		if err := s.assignCode("sites", sites[i].ID, "SITE"); err != nil {
			return err
		}
	}
	var apps []Application
	if err := s.DB.Where("code IS NULL OR code = ''").Find(&apps).Error; err != nil {
		return err
	}
	for i := range apps {
		if err := s.assignCode("applications", apps[i].ID, "APP"); err != nil {
			return err
		}
	}
	var certs []Certificate
	if err := s.DB.Where("code IS NULL OR code = ''").Find(&certs).Error; err != nil {
		return err
	}
	for i := range certs {
		if err := s.assignCode("certificates", certs[i].ID, "CERT"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) assignCode(table string, id uint, prefix string) error {
	for attempt := 0; attempt < 20; attempt++ {
		code, err := NewBusinessCode(prefix)
		if err != nil {
			return err
		}
		res := s.DB.Table(table).Where("id = ?", id).UpdateColumn("code", code)
		if res.Error == nil {
			return nil
		}
		if !isUniqueViolation(res.Error) {
			return res.Error
		}
	}
	return fmt.Errorf("could not allocate unique business code for %s %d", table, id)
}

func (s *Store) backfillAuditResourceCodes() error {
	if err := s.backfillBusinessCodes(); err != nil {
		return err
	}
	type mapping struct {
		resourceType string
		table        string
	}
	for _, m := range []mapping{
		{resourceType: "site", table: "sites"},
		{resourceType: "application", table: "applications"},
		{resourceType: "certificate", table: "certificates"},
	} {
		query := fmt.Sprintf(`UPDATE audit_logs
			SET resource_id = (SELECT code FROM %s WHERE %s.id = CAST(audit_logs.resource_id AS INTEGER))
			WHERE resource_type = ?
			  AND resource_id <> ''
			  AND resource_id NOT GLOB '*[^0-9]*'
			  AND EXISTS (SELECT 1 FROM %s WHERE %s.id = CAST(audit_logs.resource_id AS INTEGER)
			              AND %s.code IS NOT NULL AND %s.code <> '')`, m.table, m.table, m.table, m.table, m.table, m.table)
		if err := s.DB.Exec(query, m.resourceType).Error; err != nil {
			return err
		}
	}
	return nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}
