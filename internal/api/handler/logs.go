package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/proxygate/proxygate/internal/api/response"
	"github.com/proxygate/proxygate/internal/apperr"
	applog "github.com/proxygate/proxygate/internal/log"
	"github.com/proxygate/proxygate/internal/storage"
)

type streamFilter struct {
	types map[string]bool
}

func accessFilter(r *http.Request, siteID uint, page, pageSize int) applog.Filter {
	f := applog.Filter{Page: page, PageSize: pageSize, SiteID: siteID}
	q := r.URL.Query()
	if siteID == 0 {
		if v := q.Get("site_id"); v != "" {
			fmt.Sscanf(v, "%d", &f.SiteID)
		}
	}
	f.ClientIP = q.Get("ip")
	f.Method = q.Get("method")
	f.Path = q.Get("q")
	f.RequestID = q.Get("request_id")
	fmt.Sscanf(q.Get("status"), "%d", &f.Status)
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.From = &t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.To = &t
		}
	}
	return f
}

// AccessLogs queries the in-memory ring or a daily JSONL file.
func (s *Server) AccessLogs(w http.ResponseWriter, r *http.Request) {
	page, pageSize := response.PageQuery(r)
	var siteID uint
	if code := strings.TrimSpace(r.URL.Query().Get("site_code")); code != "" {
		row, err := s.Sites.GetByCode(r.Context(), code)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		siteID = row.ID
	}
	filter := accessFilter(r, siteID, page, pageSize)
	if date := r.URL.Query().Get("date"); date != "" {
		items, total, err := s.Access.QueryFile(date, filter)
		if err != nil {
			s.fail(w, r, apperr.Invalidf("cannot query access log: %v", err))
			return
		}
		response.OK(w, response.Page{Items: items, Total: total, Page: page, PageSize: pageSize})
		return
	}
	items, total := s.Access.Query(filter)
	if total == 0 && !hasAccessQuery(r) {
		for d := 0; d < 2; d++ {
			date := time.Now().AddDate(0, 0, -d).Format("2006-01-02")
			fileItems, fileTotal, err := s.Access.QueryFile(date, filter)
			if err == nil && fileTotal > 0 {
				response.OK(w, response.Page{Items: fileItems, Total: fileTotal, Page: page, PageSize: pageSize})
				return
			}
		}
	}
	response.OK(w, response.Page{Items: items, Total: total, Page: page, PageSize: pageSize})
}

func hasAccessQuery(r *http.Request) bool {
	q := r.URL.Query()
	for _, key := range []string{"site_id", "site_code", "ip", "status", "q", "method", "request_id", "from", "to"} {
		if q.Get(key) != "" {
			return true
		}
	}
	return false
}

// SystemLogs queries persisted system logs.
func (s *Server) SystemLogs(w http.ResponseWriter, r *http.Request) {
	page, pageSize := response.PageQuery(r)
	f := storage.SystemLogFilter{Level: r.URL.Query().Get("level"), Category: r.URL.Query().Get("category"), Page: page, PageSize: pageSize}
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.From = &t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.To = &t
		}
	}
	rows, total, err := s.Store.ListSystemLogs(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, response.Page{Items: rows, Total: total, Page: page, PageSize: pageSize})
}

// AuditLogs queries operation audit records.
func (s *Server) AuditLogs(w http.ResponseWriter, r *http.Request) {
	page, pageSize := response.PageQuery(r)
	f := storage.AuditFilter{Action: r.URL.Query().Get("action"), Resource: r.URL.Query().Get("resource"), Page: page, PageSize: pageSize}
	if v := r.URL.Query().Get("user_id"); v != "" {
		var id uint
		if _, err := fmt.Sscanf(v, "%d", &id); err == nil && id > 0 {
			f.UserID = &id
		}
	}
	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.From = &t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.To = &t
		}
	}
	rows, total, err := s.Store.ListAudit(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	response.OK(w, response.Page{Items: rows, Total: total, Page: page, PageSize: pageSize})
}

// Stream serves Server-Sent Events with access and system logs.
func (s *Server) Stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.fail(w, r, apperr.Invalidf("streaming is not supported"))
		return
	}
	if s.sse.Add(1) > 32 {
		s.sse.Add(-1)
		s.fail(w, r, apperr.New(apperr.Unavailable, "too many log streams"))
		return
	}
	defer s.sse.Add(-1)
	ch, unsubscribe := s.Hub.Subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	flusher.Flush()

	types := map[string]bool{"access": true, "system": true}
	if raw := r.URL.Query().Get("types"); raw != "" {
		types = map[string]bool{}
		for _, t := range strings.Split(raw, ",") {
			types[strings.TrimSpace(t)] = true
		}
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !types[ev.Type] {
				continue
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// DownloadAccessLog streams a daily access log file.
func (s *Server) DownloadAccessLog(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	path, err := s.Access.FileForDate(date)
	if err != nil {
		s.fail(w, r, apperr.NotFoundf("no access log for %s", date))
		return
	}
	s.audit(r, "download_access_log", "access_log", nil, "success", map[string]any{"date": date})
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="access-%s.jsonl"`, date))
	f, err := os.Open(path)
	if err != nil {
		s.fail(w, r, apperr.NotFoundf("no access log for %s", date))
		return
	}
	defer f.Close()
	w.WriteHeader(http.StatusOK)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var e applog.Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		e.Path = applog.RedactRequestURI(e.Path)
		line, err := json.Marshal(e)
		if err != nil {
			continue
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			return
		}
	}
}
