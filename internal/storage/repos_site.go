package storage

import (
	"context"

	"gorm.io/gorm"
)

// ListSites returns all sites with their application, nodes and certificate.
func (s *Store) ListSites(ctx context.Context) ([]Site, error) {
	var out []Site
	err := s.DB.WithContext(ctx).
		Preload("Application").Preload("Application.Nodes").
		Preload("Certificate").
		Order("id").Find(&out).Error
	return out, err
}

// GetSite loads one site with associations.
func (s *Store) GetSite(ctx context.Context, id uint) (*Site, error) {
	var site Site
	err := s.DB.WithContext(ctx).
		Preload("Application").Preload("Application.Nodes").
		Preload("Certificate").
		First(&site, id).Error
	if err != nil {
		return nil, wrapErr(err)
	}
	return &site, nil
}

// CreateSite inserts a site.
func (s *Store) CreateSite(ctx context.Context, site *Site) error {
	return s.DB.WithContext(ctx).Omit("Application", "Certificate").Create(site).Error
}

// UpdateSite saves all mutable columns; associations are managed elsewhere.
func (s *Store) UpdateSite(ctx context.Context, site *Site) error {
	return s.DB.WithContext(ctx).Omit("Application", "Certificate").Save(site).Error
}

// DeleteSite removes a site.
func (s *Store) DeleteSite(ctx context.Context, id uint) error {
	res := s.DB.WithContext(ctx).Delete(&Site{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListApplications returns all application groups with ordered nodes.
func (s *Store) ListApplications(ctx context.Context) ([]Application, error) {
	var out []Application
	err := s.DB.WithContext(ctx).
		Preload("Nodes", func(db *gorm.DB) *gorm.DB { return db.Order("priority, id") }).
		Order("id").Find(&out).Error
	return out, err
}

// GetApplication loads one application with nodes.
func (s *Store) GetApplication(ctx context.Context, id uint) (*Application, error) {
	var up Application
	err := s.DB.WithContext(ctx).
		Preload("Nodes", func(db *gorm.DB) *gorm.DB { return db.Order("priority, id") }).
		First(&up, id).Error
	if err != nil {
		return nil, wrapErr(err)
	}
	return &up, nil
}

// CreateApplication inserts an application and its nodes atomically.
func (s *Store) CreateApplication(ctx context.Context, up *Application) error {
	return s.tx(func(tx *gorm.DB) error {
		return tx.WithContext(ctx).Create(up).Error
	})
}

// UpdateApplication saves application fields.
func (s *Store) UpdateApplication(ctx context.Context, up *Application) error {
	return s.DB.WithContext(ctx).Omit("Nodes").Save(up).Error
}

// UpdateApplicationWithNodes saves group fields and reconciles its nodes in one
// transaction. Nodes absent from the list are deleted.
func (s *Store) UpdateApplicationWithNodes(ctx context.Context, up *Application) error {
	return s.tx(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Omit("Nodes").Save(up).Error; err != nil {
			return err
		}
		ids := make([]uint, 0, len(up.Nodes))
		for i := range up.Nodes {
			n := &up.Nodes[i]
			n.ApplicationID = up.ID
			if n.ID == 0 {
				if err := tx.WithContext(ctx).Create(n).Error; err != nil {
					return err
				}
			} else if err := tx.WithContext(ctx).Save(n).Error; err != nil {
				return err
			}
			ids = append(ids, n.ID)
		}
		if len(ids) > 0 {
			if err := tx.WithContext(ctx).
				Where("application_id = ? AND id NOT IN ?", up.ID, ids).
				Delete(&ApplicationNode{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteApplication deletes a group; sites referencing it make this fail.
func (s *Store) DeleteApplication(ctx context.Context, id uint) error {
	res := s.DB.WithContext(ctx).Delete(&Application{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetNode loads one application node.
func (s *Store) GetNode(ctx context.Context, applicationID, nodeID uint) (*ApplicationNode, error) {
	var n ApplicationNode
	err := s.DB.WithContext(ctx).Where("application_id = ? AND id = ?", applicationID, nodeID).First(&n).Error
	if err != nil {
		return nil, wrapErr(err)
	}
	return &n, nil
}

// CreateNode inserts a node.
func (s *Store) CreateNode(ctx context.Context, n *ApplicationNode) error {
	return s.DB.WithContext(ctx).Create(n).Error
}

// UpdateNode saves a node.
func (s *Store) UpdateNode(ctx context.Context, n *ApplicationNode) error {
	return s.DB.WithContext(ctx).Save(n).Error
}

// DeleteNode removes a node.
func (s *Store) DeleteNode(ctx context.Context, applicationID, nodeID uint) error {
	res := s.DB.WithContext(ctx).Where("application_id = ? AND id = ?", applicationID, nodeID).Delete(&ApplicationNode{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetNodeHealth updates the persisted health flag.
func (s *Store) SetNodeHealth(ctx context.Context, id uint, healthy bool) error {
	return s.DB.WithContext(ctx).Model(&ApplicationNode{}).Where("id = ?", id).Update("healthy", healthy).Error
}

// ListCertificates returns all certificates.
func (s *Store) ListCertificates(ctx context.Context) ([]Certificate, error) {
	var out []Certificate
	err := s.DB.WithContext(ctx).Order("id").Find(&out).Error
	return out, err
}

// GetCertificate loads one certificate.
func (s *Store) GetCertificate(ctx context.Context, id uint) (*Certificate, error) {
	var c Certificate
	if err := s.DB.WithContext(ctx).First(&c, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &c, nil
}

// ListCertificatesDue returns renewable certificates whose next check is due.
func (s *Store) ListCertificatesDue(ctx context.Context) ([]Certificate, error) {
	var out []Certificate
	err := s.DB.WithContext(ctx).
		Where("certificate_type = ? AND auto_renew = ?", "acme", true).
		Find(&out).Error
	return out, err
}

// ListPendingCertificates returns ACME orders interrupted by a process restart.
func (s *Store) ListPendingCertificates(ctx context.Context) ([]Certificate, error) {
	var out []Certificate
	err := s.DB.WithContext(ctx).
		Where("certificate_type = ? AND status = ?", "acme", "pending").
		Find(&out).Error
	return out, err
}

// CreateCertificate inserts a certificate record.
func (s *Store) CreateCertificate(ctx context.Context, c *Certificate) error {
	return s.DB.WithContext(ctx).Create(c).Error
}

// UpdateCertificate saves a certificate record.
func (s *Store) UpdateCertificate(ctx context.Context, c *Certificate) error {
	return s.DB.WithContext(ctx).Save(c).Error
}

// DeleteCertificate removes a record.
func (s *Store) DeleteCertificate(ctx context.Context, id uint) error {
	res := s.DB.WithContext(ctx).Delete(&Certificate{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// CountSitesByCertificate counts sites using a certificate.
func (s *Store) CountSitesByCertificate(ctx context.Context, id uint) (int64, error) {
	var n int64
	err := s.DB.WithContext(ctx).Model(&Site{}).Where("certificate_id = ?", id).Count(&n).Error
	return n, err
}

// ReplaceConfig replaces sites and applications atomically, preserving IDs from a
// configuration snapshot. Certificates are not touched: snapshots reference
// certificate IDs that must still exist.
func (s *Store) ReplaceConfig(ctx context.Context, sites []Site, ups []Application) error {
	return s.tx(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Exec("DELETE FROM sites").Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Exec("DELETE FROM application_nodes").Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Exec("DELETE FROM applications").Error; err != nil {
			return err
		}
		for i := range ups {
			up := ups[i]
			nodes := up.Nodes
			up.Nodes = nil
			if err := tx.WithContext(ctx).Omit("Nodes").Create(&up).Error; err != nil {
				return err
			}
			for j := range nodes {
				node := nodes[j]
				node.ApplicationID = up.ID
				if err := tx.WithContext(ctx).Create(&node).Error; err != nil {
					return err
				}
			}
		}
		for i := range sites {
			site := sites[i]
			if err := tx.WithContext(ctx).Omit("Application", "Certificate").Create(&site).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
