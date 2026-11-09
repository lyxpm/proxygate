package storage

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateConfigVersion stores a snapshot with a monotonically increasing version.
func (s *Store) CreateConfigVersion(ctx context.Context, v *ConfigVersion) error {
	return s.tx(func(tx *gorm.DB) error {
		var max int
		if err := tx.WithContext(ctx).Raw("SELECT COALESCE(MAX(version), 0) FROM config_versions").Scan(&max).Error; err != nil {
			return err
		}
		v.Version = max + 1
		return tx.WithContext(ctx).Create(v).Error
	})
}

// ListConfigVersions returns snapshots newest first, optionally with the payload.
func (s *Store) ListConfigVersions(ctx context.Context, limit int, withSnapshot bool) ([]ConfigVersion, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := s.DB.WithContext(ctx).Order("version DESC").Limit(limit)
	if !withSnapshot {
		q = q.Omit("snapshot")
	}
	var out []ConfigVersion
	err := q.Find(&out).Error
	return out, err
}

// GetConfigVersion loads a snapshot by version number.
func (s *Store) GetConfigVersion(ctx context.Context, version int) (*ConfigVersion, error) {
	var v ConfigVersion
	if err := s.DB.WithContext(ctx).Where("version = ?", version).First(&v).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &v, nil
}

// LatestConfigVersion returns the newest snapshot.
func (s *Store) LatestConfigVersion(ctx context.Context) (*ConfigVersion, error) {
	var v ConfigVersion
	if err := s.DB.WithContext(ctx).Order("version DESC").First(&v).Error; err != nil {
		return nil, wrapErr(err)
	}
	return &v, nil
}

// DeleteConfigVersion removes one snapshot.
func (s *Store) DeleteConfigVersion(ctx context.Context, version int) (int64, error) {
	res := s.DB.WithContext(ctx).Where("version = ?", version).Delete(&ConfigVersion{})
	return res.RowsAffected, res.Error
}

// DeleteConfigVersions removes multiple snapshots in one statement.
func (s *Store) DeleteConfigVersions(ctx context.Context, versions []int) (int64, error) {
	if len(versions) == 0 {
		return 0, nil
	}
	res := s.DB.WithContext(ctx).Where("version IN ?", versions).Delete(&ConfigVersion{})
	return res.RowsAffected, res.Error
}

// PruneConfigVersions keeps the newest keep snapshots and deletes older rows.
func (s *Store) PruneConfigVersions(ctx context.Context, keep int) (int64, error) {
	if keep < 1 {
		return 0, nil
	}
	res := s.DB.WithContext(ctx).Exec(
		`DELETE FROM config_versions WHERE version NOT IN (SELECT version FROM config_versions ORDER BY version DESC LIMIT ?)`, keep)
	return res.RowsAffected, res.Error
}

// TrafficDelta is one minute-bucket increment.
type TrafficDelta struct {
	SiteID          uint
	Bucket          time.Time
	RequestCount    int64
	InboundBytes    int64
	OutboundBytes   int64
	Status2xx       int64
	Status3xx       int64
	Status4xx       int64
	Status5xx       int64
	ResponseSum     int64
	ResponseMax     int64
	LatencyRequests int64
	Latency         LatencyHistogram
}

// UpsertTraffic increments the minute bucket for a site.
func (s *Store) UpsertTraffic(ctx context.Context, d TrafficDelta) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return upsertTraffic(ctx, tx, d)
	})
}

// UpsertTrafficBatch applies a flush in one transaction.
func (s *Store) UpsertTrafficBatch(ctx context.Context, deltas []TrafficDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, d := range deltas {
			if err := upsertTraffic(ctx, tx, d); err != nil {
				return err
			}
		}
		return nil
	})
}

func upsertTraffic(ctx context.Context, tx *gorm.DB, d TrafficDelta) error {
	var row TrafficStat
	err := tx.WithContext(ctx).Where(
		"site_id = ? AND interval_type = ? AND bucket_time = ?",
		d.SiteID, "minute", d.Bucket.UTC(),
	).First(&row).Error
	if err == nil {
		if row.LatencyRequests == 0 && d.LatencyRequests > 0 {
			// Legacy rows have no per-request split. Once the first non-upgrade
			// sample arrives, discard the old mixed latency aggregate instead
			// of carrying WebSocket connection lifetimes into HTTP latency.
			return tx.WithContext(ctx).Model(&TrafficStat{}).Where("id = ?", row.ID).Updates(map[string]any{
				"request_count":     gorm.Expr("request_count + ?", d.RequestCount),
				"inbound_bytes":     gorm.Expr("inbound_bytes + ?", d.InboundBytes),
				"outbound_bytes":    gorm.Expr("outbound_bytes + ?", d.OutboundBytes),
				"status_2xx":        gorm.Expr("status_2xx + ?", d.Status2xx),
				"status_3xx":        gorm.Expr("status_3xx + ?", d.Status3xx),
				"status_4xx":        gorm.Expr("status_4xx + ?", d.Status4xx),
				"status_5xx":        gorm.Expr("status_5xx + ?", d.Status5xx),
				"response_time_sum": d.ResponseSum,
				"response_time_max": d.ResponseMax,
				"latency_requests":  d.LatencyRequests,
				"latency_hist":      d.Latency.Encode(),
			}).Error
		}
		hist := DecodeLatencyHistogram(row.LatencyHist)
		hist.AddHistogram(d.Latency)
		return tx.WithContext(ctx).Model(&TrafficStat{}).Where("id = ?", row.ID).Updates(map[string]any{
			"request_count":     gorm.Expr("request_count + ?", d.RequestCount),
			"inbound_bytes":     gorm.Expr("inbound_bytes + ?", d.InboundBytes),
			"outbound_bytes":    gorm.Expr("outbound_bytes + ?", d.OutboundBytes),
			"status_2xx":        gorm.Expr("status_2xx + ?", d.Status2xx),
			"status_3xx":        gorm.Expr("status_3xx + ?", d.Status3xx),
			"status_4xx":        gorm.Expr("status_4xx + ?", d.Status4xx),
			"status_5xx":        gorm.Expr("status_5xx + ?", d.Status5xx),
			"response_time_sum": gorm.Expr("response_time_sum + ?", d.ResponseSum),
			"response_time_max": gorm.Expr("MAX(response_time_max, ?)", d.ResponseMax),
			"latency_requests":  gorm.Expr("latency_requests + ?", d.LatencyRequests),
			"latency_hist":      hist.Encode(),
		}).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	row = trafficStatRow(d)
	row.LatencyHist = d.Latency.Encode()
	return tx.WithContext(ctx).Create(&row).Error
}

func trafficStatRow(d TrafficDelta) TrafficStat {
	latencyRequests := d.LatencyRequests
	if latencyRequests == 0 && d.Latency.Count() > 0 {
		latencyRequests = d.Latency.Count()
	}
	return TrafficStat{
		SiteID: d.SiteID, IntervalType: "minute", BucketTime: d.Bucket.UTC(),
		RequestCount: d.RequestCount, InboundBytes: d.InboundBytes, OutboundBytes: d.OutboundBytes,
		Status2xx: d.Status2xx, Status3xx: d.Status3xx, Status4xx: d.Status4xx, Status5xx: d.Status5xx,
		ResponseTimeSum: d.ResponseSum, ResponseTimeMax: d.ResponseMax,
		LatencyRequests: latencyRequests,
	}
}

// QueryTraffic returns minute rows for a site or all sites (siteID 0).
func (s *Store) QueryTraffic(ctx context.Context, siteID uint, from, to time.Time) ([]TrafficStat, error) {
	q := s.DB.WithContext(ctx).Where("interval_type = ? AND bucket_time >= ? AND bucket_time < ?", "minute", from.UTC(), to.UTC())
	if siteID > 0 {
		q = q.Where("site_id = ?", siteID)
	}
	var out []TrafficStat
	err := q.Order("bucket_time").Find(&out).Error
	return out, err
}

// TrafficTotals sums all sites over a range.
func (s *Store) TrafficTotals(ctx context.Context, from, to time.Time) (TrafficStat, error) {
	rows, err := s.QueryTraffic(ctx, 0, from, to)
	if err != nil {
		return TrafficStat{}, err
	}
	var out TrafficStat
	var hist LatencyHistogram
	for _, r := range rows {
		out.RequestCount += r.RequestCount
		out.InboundBytes += r.InboundBytes
		out.OutboundBytes += r.OutboundBytes
		out.Status2xx += r.Status2xx
		out.Status3xx += r.Status3xx
		out.Status4xx += r.Status4xx
		out.Status5xx += r.Status5xx
		if r.LatencyRequests > 0 {
			out.LatencyRequests += r.LatencyRequests
			out.ResponseTimeSum += r.ResponseTimeSum
			if r.ResponseTimeMax > out.ResponseTimeMax {
				out.ResponseTimeMax = r.ResponseTimeMax
			}
			hist.AddHistogram(DecodeLatencyHistogram(r.LatencyHist))
		}
	}
	out.LatencyHist = hist.Encode()
	return out, nil
}

// SiteTraffic is a per-site aggregate.
type SiteTraffic struct {
	SiteID          uint
	RequestCount    int64
	InboundBytes    int64
	OutboundBytes   int64
	Status2xx       int64
	Status3xx       int64
	Status4xx       int64
	Status5xx       int64
	ResponseSum     int64
	LatencyRequests int64
	Latency         LatencyHistogram
}

// TrafficBySite groups traffic per site over a range.
func (s *Store) TrafficBySite(ctx context.Context, from, to time.Time) ([]SiteTraffic, error) {
	rows, err := s.QueryTraffic(ctx, 0, from, to)
	if err != nil {
		return nil, err
	}
	index := make(map[uint]*SiteTraffic)
	var order []uint
	for _, r := range rows {
		t := index[r.SiteID]
		if t == nil {
			t = &SiteTraffic{SiteID: r.SiteID}
			index[r.SiteID] = t
			order = append(order, r.SiteID)
		}
		t.RequestCount += r.RequestCount
		t.InboundBytes += r.InboundBytes
		t.OutboundBytes += r.OutboundBytes
		t.Status2xx += r.Status2xx
		t.Status3xx += r.Status3xx
		t.Status4xx += r.Status4xx
		t.Status5xx += r.Status5xx
		if r.LatencyRequests > 0 {
			t.LatencyRequests += r.LatencyRequests
			t.ResponseSum += r.ResponseTimeSum
			t.Latency.AddHistogram(DecodeLatencyHistogram(r.LatencyHist))
		}
	}
	out := make([]SiteTraffic, 0, len(order))
	for _, id := range order {
		out = append(out, *index[id])
	}
	return out, nil
}

// DeleteTrafficBefore prunes old minute buckets.
func (s *Store) DeleteTrafficBefore(ctx context.Context, before time.Time) (int64, error) {
	res := s.DB.WithContext(ctx).Where("bucket_time < ?", before.UTC()).Delete(&TrafficStat{})
	return res.RowsAffected, res.Error
}

// InsertAudit appends an audit record.
func (s *Store) InsertAudit(ctx context.Context, a *AuditLog) error {
	return s.DB.WithContext(ctx).Create(a).Error
}

// AuditFilter narrows an audit log query.
type AuditFilter struct {
	Action   string
	Resource string
	UserID   *uint
	From, To *time.Time
	Page     int
	PageSize int
}

// ListAudit returns audit rows and the total count.
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditLog, int64, error) {
	q := s.DB.WithContext(ctx).Model(&AuditLog{})
	if f.Action != "" {
		q = q.Where("action = ?", f.Action)
	}
	if f.Resource != "" {
		q = q.Where("resource_type = ?", f.Resource)
	}
	if f.UserID != nil {
		q = q.Where("user_id = ?", *f.UserID)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", f.From.UTC())
	}
	if f.To != nil {
		q = q.Where("created_at < ?", f.To.UTC())
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 200 {
		f.PageSize = 20
	}
	var out []AuditLog
	err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&out).Error
	return out, total, err
}

// InsertSystemLog appends a system log.
func (s *Store) InsertSystemLog(ctx context.Context, l *SystemLog) error {
	return s.DB.WithContext(ctx).Create(l).Error
}

// SystemLogFilter narrows a system log query.
type SystemLogFilter struct {
	Level    string
	Category string
	From, To *time.Time
	Page     int
	PageSize int
}

// ListSystemLogs returns system logs and total count.
func (s *Store) ListSystemLogs(ctx context.Context, f SystemLogFilter) ([]SystemLog, int64, error) {
	q := s.DB.WithContext(ctx).Model(&SystemLog{})
	if f.Level != "" {
		q = q.Where("level = ?", f.Level)
	}
	if f.Category != "" {
		q = q.Where("category = ?", f.Category)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", f.From.UTC())
	}
	if f.To != nil {
		q = q.Where("created_at < ?", f.To.UTC())
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 200 {
		f.PageSize = 20
	}
	var out []SystemLog
	err := q.Order("id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&out).Error
	return out, total, err
}

// DeleteSystemLogsBefore prunes old system logs.
func (s *Store) DeleteSystemLogsBefore(ctx context.Context, before time.Time) (int64, error) {
	res := s.DB.WithContext(ctx).Where("created_at < ?", before.UTC()).Delete(&SystemLog{})
	return res.RowsAffected, res.Error
}

// DeleteAuditBefore prunes old audit logs.
func (s *Store) DeleteAuditBefore(ctx context.Context, before time.Time) (int64, error) {
	res := s.DB.WithContext(ctx).Where("created_at < ?", before.UTC()).Delete(&AuditLog{})
	return res.RowsAffected, res.Error
}

// GetSetting returns a setting value.
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var v Setting
	err := s.DB.WithContext(ctx).Where("key = ?", key).First(&v).Error
	if err == gorm.ErrRecordNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v.Value, true, nil
}

// SetSetting upserts a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	row := Setting{Key: key, Value: value, UpdatedAt: time.Now().UTC()}
	return s.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]any{"value": value, "updated_at": row.UpdatedAt}),
	}).Create(&row).Error
}

// AllSettings returns every setting.
func (s *Store) AllSettings(ctx context.Context) (map[string]string, error) {
	var rows []Setting
	if err := s.DB.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

// InsertCertificateLog appends one certificate log line.
func (s *Store) InsertCertificateLog(ctx context.Context, l *CertificateLog) error {
	return s.DB.WithContext(ctx).Create(l).Error
}

// ListCertificateLogs returns the newest log lines for a certificate.
func (s *Store) ListCertificateLogs(ctx context.Context, certID uint, limit int) ([]CertificateLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []CertificateLog
	err := s.DB.WithContext(ctx).Where("certificate_id = ?", certID).Order("id DESC").Limit(limit).Find(&out).Error
	return out, err
}

// CountCertificatesUsingDNSProvider counts certificates bound to a provider.
func (s *Store) CountCertificatesUsingDNSProvider(ctx context.Context, id uint) (int64, error) {
	var n int64
	err := s.DB.WithContext(ctx).Model(&Certificate{}).Where("dns_provider_id = ?", id).Count(&n).Error
	return n, err
}

// CountCertificatesUsingAccount counts certificates bound to an ACME account.
func (s *Store) CountCertificatesUsingAccount(ctx context.Context, id uint) (int64, error) {
	var n int64
	err := s.DB.WithContext(ctx).Model(&Certificate{}).Where("acme_account_id = ?", id).Count(&n).Error
	return n, err
}
