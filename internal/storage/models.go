// Package storage owns persistence: SQLite connection, migrations and
// repositories. No other package imports the ORM types directly; services map
// them to domain structs at the boundary.
package storage

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// StringList is a []string persisted as a JSON array.
type StringList []string

func (l StringList) Value() (driver.Value, error) {
	if l == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]string(l))
	return string(b), err
}

func (l *StringList) Scan(v any) error {
	var raw []byte
	switch x := v.(type) {
	case nil:
		*l = nil
		return nil
	case string:
		raw = []byte(x)
	case []byte:
		raw = x
	default:
		return fmt.Errorf("StringList: unsupported type %T", v)
	}
	if len(raw) == 0 {
		*l = nil
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	*l = out
	return nil
}

// User is an administrator account.
type User struct {
	ID           uint   `gorm:"primaryKey"`
	Username     string `gorm:"uniqueIndex;size:64;not null"`
	PasswordHash string `gorm:"size:255;not null"`
	Enabled      bool   `gorm:"not null;default:true"`
	TOTPSecret   string `gorm:"column:totp_secret;size:255;not null;default:''"`
	TOTPEnabled  bool   `gorm:"column:totp_enabled;not null;default:false"`
	LastTOTPStep int64  `gorm:"column:last_totp_step;not null;default:0"`
	Role         string `gorm:"size:16;not null;default:'admin'"`
	LastLoginAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Session is a server-side login session. Only the hash of the cookie token is stored.
type Session struct {
	TokenHash  string    `gorm:"primaryKey;size:64"`
	UserID     uint      `gorm:"index;not null"`
	ExpiresAt  time.Time `gorm:"index;not null"`
	ClientIP   string    `gorm:"size:64"`
	UserAgent  string    `gorm:"size:255"`
	LastSeenAt *time.Time
	CreatedAt  time.Time
}

// Site is a reverse-proxy virtual host.
type Site struct {
	ID            uint   `gorm:"primaryKey"`
	Name          string `gorm:"uniqueIndex;size:128;not null"`
	Enabled       bool   `gorm:"index;not null;default:true"`
	ListenPort    int    `gorm:"not null;default:80"`
	Domains       StringList
	Protocol      string `gorm:"size:8;not null;default:http"`
	ApplicationID uint   `gorm:"index;not null"`
	CertificateID *uint  `gorm:"index"`
	RedirectHTTPS bool   `gorm:"column:redirect_https;not null;default:false"`
	ProxyTimeout  int    `gorm:"not null;default:30"`
	MaxBodySize   int64  `gorm:"not null;default:0"`
	ExtraConfig   string `gorm:"type:text;not null;default:'{}'"`
	Remark        string `gorm:"size:255"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Application   *Application `gorm:"foreignKey:ApplicationID"`
	Certificate   *Certificate `gorm:"foreignKey:CertificateID"`
}

// Application is a group of backend nodes with a load-balancing strategy.
type Application struct {
	ID                  uint   `gorm:"primaryKey"`
	Name                string `gorm:"uniqueIndex;size:128;not null"`
	Strategy            string `gorm:"size:32;not null;default:round_robin"`
	Enabled             bool   `gorm:"not null;default:true"`
	HealthCheckEnabled  bool   `gorm:"not null;default:true"`
	HealthCheckType     string `gorm:"size:8;not null;default:http"`
	HealthCheckPath     string `gorm:"size:255;not null;default:/"`
	HealthCheckInterval int    `gorm:"not null;default:10"`
	HealthCheckTimeout  int    `gorm:"not null;default:3"`
	HealthyThreshold    int    `gorm:"not null;default:2"`
	UnhealthyThreshold  int    `gorm:"not null;default:3"`
	PassiveFailureLimit int    `gorm:"not null;default:3"`
	PassiveCooldown     int    `gorm:"not null;default:30"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Nodes               []ApplicationNode `gorm:"foreignKey:ApplicationID"`
}

// ApplicationNode is one backend address.
type ApplicationNode struct {
	ID            uint   `gorm:"primaryKey"`
	ApplicationID uint   `gorm:"index;not null"`
	Address       string `gorm:"size:255;not null"`
	Weight        int    `gorm:"not null;default:1"`
	Priority      int    `gorm:"not null;default:0"`
	Enabled       bool   `gorm:"not null;default:true"`
	Healthy       bool   `gorm:"not null;default:true"`
	TLSVerify     bool   `gorm:"not null;default:true"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Certificate is an uploaded or ACME-issued certificate. Private keys live in
// files, never in this table.
type Certificate struct {
	ID              uint   `gorm:"primaryKey"`
	Name            string `gorm:"uniqueIndex;size:128;not null"`
	Domains         StringList
	Issuer          string     `gorm:"size:255"`
	CertificateType string     `gorm:"size:16;not null"` // acme | upload
	ChallengeType   string     `gorm:"size:16"`          // http01 | dns01
	KeyType         string     `gorm:"size:16;not null;default:P256"`
	CertificatePath string     `gorm:"size:512;not null"`
	PrivateKeyPath  string     `gorm:"size:512;not null"`
	ExpiresAt       *time.Time `gorm:"index"`
	AutoRenew       bool       `gorm:"not null;default:false"`
	Status          string     `gorm:"size:32;not null;default:pending"`
	ACMEAccountID   *uint      `gorm:"column:acme_account_id;index"`
	DNSProviderID   *uint      `gorm:"column:dns_provider_id;index"`
	LastRenewAt     *time.Time
	RenewAttempts   int `gorm:"not null;default:0"`
	NextRenewAt     *time.Time
	LastError       string `gorm:"type:text"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ACMEAccount is a registered ACME account. The account key is a 0600 file.
type ACMEAccount struct {
	ID             uint   `gorm:"primaryKey"`
	Email          string `gorm:"size:255;not null"`
	CAURL          string `gorm:"column:ca_url;size:512;not null"`
	AccountKeyPath string `gorm:"column:account_key_path;size:512;not null"`
	Registration   string `gorm:"type:text"`
	Status         string `gorm:"size:32;not null;default:valid"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// DNSProvider stores DNS-01 provider settings; credentials are AES-GCM encrypted.
type DNSProvider struct {
	ID                   uint   `gorm:"primaryKey"`
	Name                 string `gorm:"uniqueIndex;size:128;not null"`
	ProviderType         string `gorm:"size:32;not null"`
	CredentialsEncrypted string `gorm:"type:text;not null"`
	Enabled              bool   `gorm:"not null;default:true"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// ConfigVersion is an immutable snapshot of the runtime-relevant configuration.
type ConfigVersion struct {
	ID          uint      `gorm:"primaryKey"`
	Version     int       `gorm:"uniqueIndex;autoIncrement:false;not null"`
	Snapshot    string    `gorm:"type:text;not null"`
	Checksum    string    `gorm:"size:64;not null"`
	OperatorID  *uint     `gorm:"column:operator_id"`
	Description string    `gorm:"size:255"`
	CreatedAt   time.Time `gorm:"index"`
}

// TrafficStat is a pre-aggregated traffic bucket.
type TrafficStat struct {
	ID              uint      `gorm:"primaryKey"`
	SiteID          uint      `gorm:"uniqueIndex:idx_traffic_bucket;not null"`
	IntervalType    string    `gorm:"uniqueIndex:idx_traffic_bucket;size:8;not null"`
	BucketTime      time.Time `gorm:"uniqueIndex:idx_traffic_bucket;index;not null"`
	RequestCount    int64     `gorm:"not null;default:0"`
	InboundBytes    int64     `gorm:"not null;default:0"`
	OutboundBytes   int64     `gorm:"not null;default:0"`
	Status2xx       int64     `gorm:"column:status_2xx;not null;default:0"`
	Status3xx       int64     `gorm:"column:status_3xx;not null;default:0"`
	Status4xx       int64     `gorm:"column:status_4xx;not null;default:0"`
	Status5xx       int64     `gorm:"column:status_5xx;not null;default:0"`
	ResponseTimeSum int64     `gorm:"not null;default:0"` // milliseconds
	ResponseTimeMax int64     `gorm:"not null;default:0"` // milliseconds
	LatencyRequests int64     `gorm:"column:latency_requests;not null;default:0"`
	LatencyHist     string    `gorm:"column:latency_hist;not null;default:''"`
}

// AuditLog records administrative changes. Secrets are never written here.
type AuditLog struct {
	ID           uint      `gorm:"primaryKey"`
	UserID       *uint     `gorm:"index"`
	Action       string    `gorm:"size:64;not null;index"`
	ResourceType string    `gorm:"size:64;not null"`
	ResourceID   string    `gorm:"size:64"`
	ClientIP     string    `gorm:"size:64"`
	Result       string    `gorm:"size:16;not null"`
	Detail       string    `gorm:"type:text"`
	CreatedAt    time.Time `gorm:"index"`
}

// SystemLog records lifecycle events, configuration changes and failures.
type SystemLog struct {
	ID        uint      `gorm:"primaryKey"`
	Level     string    `gorm:"size:16;not null;index"`
	Category  string    `gorm:"size:32;not null;index"`
	Message   string    `gorm:"type:text;not null"`
	Fields    string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"index"`
}

// Setting is a key/value system setting.
type Setting struct {
	Key       string `gorm:"primaryKey;size:128"`
	Value     string `gorm:"type:text"`
	UpdatedAt time.Time
}

// CertificateLog is one issuance or renewal log line.
type CertificateLog struct {
	ID            uint      `gorm:"primaryKey"`
	CertificateID uint      `gorm:"index;not null"`
	Level         string    `gorm:"size:16;not null"`
	Message       string    `gorm:"type:text;not null"`
	CreatedAt     time.Time `gorm:"index"`
}

// APIToken authenticates automation with a scoped bearer token. Only the hash
// is stored; the plaintext is shown once at creation time.
type APIToken struct {
	ID         uint       `gorm:"primaryKey"`
	Name       string     `gorm:"size:128;not null"`
	TokenHash  string     `gorm:"column:token_hash;uniqueIndex;size:64;not null"`
	Scopes     StringList `gorm:"type:text;not null"`
	ExpiresAt  *time.Time `gorm:"index"`
	LastUsedAt *time.Time
	CreatedAt  time.Time `gorm:"index"`
}
