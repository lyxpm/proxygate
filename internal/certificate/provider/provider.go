// Package provider adapts lego DNS providers to the credential model used by
// Proxy Manager. Only the four providers required by the first version are
// registered; adding another is a small switch case.
package provider

import (
	"fmt"
	"strings"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/huaweicloud"
	"github.com/go-acme/lego/v4/providers/dns/tencentcloud"
)

// Credentials is the decrypted credential payload.
type Credentials struct {
	ID     string            `json:"id"`
	Secret string            `json:"secret"`
	Extra  map[string]string `json:"extra,omitempty"`
}

// Field describes one extra credential input.
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required"`
}

// Meta describes a provider for the UI.
type Meta struct {
	Name        string  `json:"name"`
	Title       string  `json:"title"`
	IDLabel     string  `json:"id_label"`
	SecretLabel string  `json:"secret_label"`
	Extra       []Field `json:"extra,omitempty"`
	DocURL      string  `json:"doc_url,omitempty"`
}

var metas = []Meta{
	{
		Name: "cloudflare", Title: "Cloudflare",
		IDLabel: "邮箱（使用 Global API Key 时填写）", SecretLabel: "API Token 或 Global API Key",
		Extra:  []Field{{Key: "api_token", Label: "API Token（推荐）"}},
		DocURL: "https://developers.cloudflare.com/fundamentals/api/get-started/create-token/",
	},
	{
		Name: "alidns", Title: "阿里云 DNS",
		IDLabel: "AccessKey ID", SecretLabel: "AccessKey Secret",
		Extra:  []Field{{Key: "region_id", Label: "Region ID", Placeholder: "cn-hangzhou"}},
		DocURL: "https://help.aliyun.com/zh/dns/",
	},
	{
		Name: "tencentcloud", Title: "腾讯云 DNSPod",
		IDLabel: "SecretId", SecretLabel: "SecretKey",
		Extra:  []Field{{Key: "region", Label: "Region", Placeholder: "ap-guangzhou"}},
		DocURL: "https://cloud.tencent.com/document/product/1427",
	},
	{
		Name: "huaweicloud", Title: "华为云 DNS",
		IDLabel: "AccessKey ID", SecretLabel: "Secret Access Key",
		Extra:  []Field{{Key: "region", Label: "Region", Placeholder: "cn-north-4"}},
		DocURL: "https://support.huaweicloud.com/dns/",
	},
}

// List returns the supported provider metadata.
func List() []Meta { return append([]Meta(nil), metas...) }

// Lookup returns metadata by name.
func Lookup(name string) (Meta, bool) {
	for _, m := range metas {
		if m.Name == name {
			return m, true
		}
	}
	return Meta{}, false
}

// New builds a lego DNS challenge provider from decrypted credentials.
func New(name string, c Credentials) (challenge.Provider, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "cloudflare":
		cfg := cloudflare.NewDefaultConfig()
		if token := strings.TrimSpace(c.Extra["api_token"]); token != "" {
			cfg.AuthToken = token
		} else if strings.TrimSpace(c.ID) == "" && strings.TrimSpace(c.Secret) != "" {
			// UI convenience: a lone secret is treated as an API Token.
			cfg.AuthToken = strings.TrimSpace(c.Secret)
		} else {
			if c.ID == "" || c.Secret == "" {
				return nil, fmt.Errorf("cloudflare requires an API token, or an email plus Global API Key")
			}
			cfg.AuthEmail = strings.TrimSpace(c.ID)
			cfg.AuthKey = strings.TrimSpace(c.Secret)
		}
		return cloudflare.NewDNSProviderConfig(cfg)
	case "alidns":
		if c.ID == "" || c.Secret == "" {
			return nil, fmt.Errorf("aliyun DNS requires AccessKey ID and Secret")
		}
		cfg := alidns.NewDefaultConfig()
		cfg.APIKey = strings.TrimSpace(c.ID)
		cfg.SecretKey = strings.TrimSpace(c.Secret)
		cfg.RegionID = "cn-hangzhou"
		if r := strings.TrimSpace(c.Extra["region_id"]); r != "" {
			cfg.RegionID = r
		}
		return alidns.NewDNSProviderConfig(cfg)
	case "tencentcloud":
		if c.ID == "" || c.Secret == "" {
			return nil, fmt.Errorf("Tencent Cloud DNS requires SecretId and SecretKey")
		}
		cfg := tencentcloud.NewDefaultConfig()
		cfg.SecretID = strings.TrimSpace(c.ID)
		cfg.SecretKey = strings.TrimSpace(c.Secret)
		cfg.Region = "ap-guangzhou"
		if r := strings.TrimSpace(c.Extra["region"]); r != "" {
			cfg.Region = r
		}
		return tencentcloud.NewDNSProviderConfig(cfg)
	case "huaweicloud":
		if c.ID == "" || c.Secret == "" {
			return nil, fmt.Errorf("Huawei Cloud DNS requires AccessKey ID and Secret Access Key")
		}
		cfg := huaweicloud.NewDefaultConfig()
		cfg.AccessKeyID = strings.TrimSpace(c.ID)
		cfg.SecretAccessKey = strings.TrimSpace(c.Secret)
		cfg.Region = "cn-north-4"
		if r := strings.TrimSpace(c.Extra["region"]); r != "" {
			cfg.Region = r
		}
		return huaweicloud.NewDNSProviderConfig(cfg)
	default:
		return nil, fmt.Errorf("unsupported DNS provider %q", name)
	}
}
