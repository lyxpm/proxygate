package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/proxygate/proxygate/internal/config"
)

type csrfTransport struct {
	jar http.CookieJar
}

func (t *csrfTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		for _, c := range t.jar.Cookies(req.URL) {
			if c.Name == "pg_csrf" {
				req.Header.Set("X-CSRF-Token", c.Value)
				break
			}
		}
	}
	return http.DefaultTransport.RoundTrip(req)
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestAppEndToEnd(t *testing.T) {
	t.Setenv("PG_DATA_DIR", t.TempDir())
	t.Setenv("PG_ADMIN_LISTEN", "127.0.0.1:0")
	t.Setenv("PG_ADMIN_PASSWORD", "admin12345")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown(context.Background())

	admin := httptest.NewServer(a.Handler())
	defer admin.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second, Transport: &csrfTransport{jar: jar}}

	post := func(path string, body any) *http.Response {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, admin.URL+"/api/v1"+path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := post("/auth/login", map[string]string{"username": "admin", "password": "admin12345"})
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status=%d body=%s", resp.StatusCode, raw)
	}
	resp.Body.Close()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello-from-backend"))
	}))
	defer backend.Close()

	upBody := map[string]any{
		"name": "pool", "strategy": "round_robin", "health_check_enabled": true,
		"health_check_type": "http", "health_check_path": "/", "health_check_interval": 5, "health_check_timeout": 2,
		"nodes": []map[string]any{{"address": backend.URL}},
	}
	resp = post("/applications", upBody)
	if resp.StatusCode != 201 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create application status=%d body=%s", resp.StatusCode, raw)
	}
	var upResp struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&upResp)
	resp.Body.Close()

	port := freePort(t)
	siteBody := map[string]any{
		"name": "e2e", "listen_port": port, "domains": []string{"e2e.test"}, "protocol": "http",
		"application_id": upResp.Data.ID, "proxy_timeout": 10,
	}
	resp = post("/sites", siteBody)
	if resp.StatusCode != 201 {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create site status=%d body=%s", resp.StatusCode, raw)
	}
	resp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	req.Host = "e2e.test"
	proxyResp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(proxyResp.Body)
	proxyResp.Body.Close()
	if string(body) != "hello-from-backend" {
		t.Fatalf("proxy body=%q", body)
	}

	// Wait for the metrics flush and verify the dashboard sees the request.
	time.Sleep(11 * time.Second)
	req, _ = http.NewRequest(http.MethodGet, admin.URL+"/api/v1/dashboard/overview", nil)
	overviewResp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer overviewResp.Body.Close()
	if overviewResp.StatusCode != 200 {
		t.Fatalf("overview status=%d", overviewResp.StatusCode)
	}
	var overview struct {
		Data struct {
			Today struct {
				Requests int64 `json:"requests"`
			} `json:"today"`
		} `json:"data"`
	}
	if err := json.NewDecoder(overviewResp.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	if overview.Data.Today.Requests < 1 {
		t.Fatalf("expected at least one request in dashboard, got %d", overview.Data.Today.Requests)
	}

	// Snapshot rollback: remember the version right after site creation,
	// change the site, then restore the snapshot.
	getJSON := func(path string, out any) {
		req, _ := http.NewRequest(http.MethodGet, admin.URL+"/api/v1"+path, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET %s status=%d body=%s", path, resp.StatusCode, raw)
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	var versions struct {
		Data struct {
			Items []struct {
				Version int `json:"Version"`
			} `json:"items"`
		} `json:"data"`
	}
	getJSON("/system/config-versions", &versions)
	if len(versions.Data.Items) == 0 {
		t.Fatal("expected config versions")
	}
	rollbackTo := versions.Data.Items[0].Version

	var siteList struct {
		Data struct {
			Items []struct {
				ID     uint   `json:"id"`
				Remark string `json:"remark"`
			} `json:"items"`
		} `json:"data"`
	}
	getJSON("/sites?page=1&page_size=10", &siteList)
	if len(siteList.Data.Items) != 1 {
		t.Fatalf("expected one site, got %d", len(siteList.Data.Items))
	}
	siteID := siteList.Data.Items[0].ID
	putBody := map[string]any{
		"name": "e2e-renamed", "listen_port": port, "domains": []string{"e2e.test"}, "protocol": "http",
		"application_id": upResp.Data.ID, "proxy_timeout": 10, "remark": "changed",
	}
	rawPut, _ := json.Marshal(putBody)
	putReq, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/sites/%d", admin.URL, siteID), strings.NewReader(string(rawPut)))
	putReq.Header.Set("Content-Type", "application/json")
	putResp, err := client.Do(putReq)
	if err != nil {
		t.Fatal(err)
	}
	putResp.Body.Close()
	if putResp.StatusCode != 200 {
		t.Fatalf("update site status=%d", putResp.StatusCode)
	}
	rollbackReq, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/system/config-versions/%d/rollback", admin.URL, rollbackTo), nil)
	rollbackResp, err := client.Do(rollbackReq)
	if err != nil {
		t.Fatal(err)
	}
	rollbackResp.Body.Close()
	if rollbackResp.StatusCode != 200 {
		t.Fatalf("rollback status=%d", rollbackResp.StatusCode)
	}
	getJSON("/sites?page=1&page_size=10", &siteList)
	if len(siteList.Data.Items) != 1 || siteList.Data.Items[0].Remark != "" {
		t.Fatalf("rollback did not restore site: %+v", siteList.Data.Items)
	}

	getJSON("/system/config-versions", &versions)
	if len(versions.Data.Items) < 2 {
		t.Fatal("expected at least two config versions")
	}
	currentVersion := versions.Data.Items[0].Version
	oldVersion := versions.Data.Items[len(versions.Data.Items)-1].Version
	deleteReq := func(version int, password string) int {
		body, _ := json.Marshal(map[string]string{"password": password})
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/system/config-versions/%d", admin.URL, version), strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := deleteReq(oldVersion, "wrong-password"); code != http.StatusBadRequest {
		t.Fatalf("wrong password status=%d", code)
	}
	if code := deleteReq(currentVersion, "admin12345"); code != http.StatusBadRequest {
		t.Fatalf("delete current version status=%d", code)
	}
	if code := deleteReq(oldVersion, "admin12345"); code != 200 {
		t.Fatalf("delete old version status=%d", code)
	}
	getJSON("/system/config-versions", &versions)
	for _, v := range versions.Data.Items {
		if v.Version == oldVersion {
			t.Fatalf("deleted version %d still listed", oldVersion)
		}
	}

	// API token: read-only token can read but not mutate; token management
	// itself requires a login session.
	rawTokenBody, _ := json.Marshal(map[string]any{"name": "ci", "read_only": true})
	tokenReq, _ := http.NewRequest(http.MethodPost, admin.URL+"/api/v1/api-tokens", strings.NewReader(string(rawTokenBody)))
	tokenReq.Header.Set("Content-Type", "application/json")
	tokenResp, err := client.Do(tokenReq)
	if err != nil {
		t.Fatal(err)
	}
	var tokenPayload struct {
		Data struct {
			Token string `json:"token"`
			Item  struct {
				ID uint `json:"id"`
			} `json:"item"`
		} `json:"data"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenPayload); err != nil {
		t.Fatal(err)
	}
	tokenResp.Body.Close()
	if tokenResp.StatusCode != 201 || !strings.HasPrefix(tokenPayload.Data.Token, "pg_") {
		t.Fatalf("create token status=%d token=%q", tokenResp.StatusCode, tokenPayload.Data.Token)
	}
	bearer := func(method, path string) *http.Response {
		req, _ := http.NewRequest(method, admin.URL+"/api/v1"+path, nil)
		req.Header.Set("Authorization", "Bearer "+tokenPayload.Data.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	readResp := bearer(http.MethodGet, "/sites?page=1&page_size=10")
	readResp.Body.Close()
	if readResp.StatusCode != 200 {
		t.Fatalf("read with token status=%d", readResp.StatusCode)
	}
	writeResp := bearer(http.MethodPost, "/sites")
	writeResp.Body.Close()
	if writeResp.StatusCode != 403 {
		t.Fatalf("write with read-only token status=%d, want 403", writeResp.StatusCode)
	}
	sessionResp := bearer(http.MethodGet, "/api-tokens")
	sessionResp.Body.Close()
	if sessionResp.StatusCode != 403 {
		t.Fatalf("token management with token status=%d, want 403", sessionResp.StatusCode)
	}
	delReq, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/api-tokens/%d", admin.URL, tokenPayload.Data.Item.ID), nil)
	delResp, err := client.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != 204 {
		t.Fatalf("delete token status=%d", delResp.StatusCode)
	}
	revoked := bearer(http.MethodGet, "/sites")
	revoked.Body.Close()
	if revoked.StatusCode != 401 {
		t.Fatalf("revoked token status=%d, want 401", revoked.StatusCode)
	}

	// Backup snapshot + staged restore: the archive must be a valid zip and
	// restore must only stage, never touch the live database.
	backupReq, _ := http.NewRequest(http.MethodPost, admin.URL+"/api/v1/system/backup", strings.NewReader(`{"password":"admin12345"}`))
	backupReq.Header.Set("Content-Type", "application/json")
	backupResp, err := client.Do(backupReq)
	if err != nil {
		t.Fatal(err)
	}
	zipBytes, err := io.ReadAll(backupResp.Body)
	backupResp.Body.Close()
	if err != nil || backupResp.StatusCode != 200 || len(zipBytes) < 4 || string(zipBytes[:2]) != "PK" {
		t.Fatalf("backup failed: status=%d len=%d err=%v", backupResp.StatusCode, len(zipBytes), err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("open backup zip: %v", err)
	}
	hasDB := false
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "logs/") {
			t.Fatalf("backup must not contain access logs: %s", f.Name)
		}
		if f.Name == "database/proxygate.db" {
			hasDB = true
		}
	}
	if !hasDB {
		t.Fatal("backup missing database")
	}
	restoreReq, _ := http.NewRequest(http.MethodPost, admin.URL+"/api/v1/system/restore", bytes.NewReader(zipBytes))
	restoreReq.Header.Set("Content-Type", "application/zip")
	restoreReq.Header.Set("X-Admin-Password", "admin12345")
	restoreResp, err := client.Do(restoreReq)
	if err != nil {
		t.Fatal(err)
	}
	restoreResp.Body.Close()
	if restoreResp.StatusCode != http.StatusAccepted {
		t.Fatalf("restore status=%d", restoreResp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "config", "restore-pending.zip")); err != nil {
		t.Fatalf("restore was not staged: %v", err)
	}
}
