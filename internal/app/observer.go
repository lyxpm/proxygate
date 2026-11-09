package app

import (
	"context"
	"fmt"
	"time"

	"github.com/proxygate/proxygate/internal/log"
	"github.com/proxygate/proxygate/internal/proxy"
	"github.com/proxygate/proxygate/internal/statistics"
	"github.com/proxygate/proxygate/internal/storage"
)

type observer struct {
	access *applog.AccessLog
	stats  *statistics.Collector
	system *applog.System
	store  *storage.Store
}

func (o *observer) RecordRequest(ev proxy.RequestEvent) {
	entry := applog.Entry{
		Time: ev.Time, SiteID: ev.SiteID, SiteName: ev.SiteName, Domain: ev.Domain,
		ClientIP: ev.ClientIP, Method: ev.Method, Path: ev.Path, Protocol: ev.Protocol,
		Status: ev.Status, DurationMS: ev.Duration.Milliseconds(), Upgraded: ev.Upgraded,
		Application:  ev.Application,
		RequestBytes: ev.RequestBytes, ResponseBytes: ev.ResponseBytes,
		UserAgent: ev.UserAgent, RequestID: ev.RequestID,
	}
	o.access.Record(entry)
	o.stats.Record(ev)
}

func (o *observer) ApplicationChanged(ev proxy.ApplicationEvent) {
	if o.store != nil {
		_ = o.store.SetNodeHealth(context.Background(), ev.NodeID, ev.Healthy)
	}
	level := "info"
	word := "已恢复"
	if !ev.Healthy {
		level = "warn"
		word = "不可用"
	}
	msg := fmt.Sprintf("节点 %s %s：%s", ev.Address, word, ev.Reason)
	o.system.Logf(context.Background(), level, "application", msg, map[string]any{
		"application_id": ev.ApplicationID, "node_id": ev.NodeID, "healthy": ev.Healthy,
		"reason": ev.Reason, "at": ev.Time.Format(time.RFC3339),
	})
}
