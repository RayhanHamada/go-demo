package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/rs/zerolog"

	"gin-clean-arch-demo/internal/adapter/cache"
	"gin-clean-arch-demo/internal/adapter/httpapi"
	"gin-clean-arch-demo/internal/adapter/sqlite"
	"gin-clean-arch-demo/internal/usecase"
)

type healthStub struct{ err error }

func (h healthStub) Ping(context.Context) error { return h.err }

// newRouter builds the full stack with an in-memory SQLite file and miniredis.
func newRouter(t *testing.T, logs io.Writer) http.Handler {
	t.Helper()
	ctx := context.Background()

	store, err := sqlite.Open(ctx, sqlite.Config{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	redis := miniredis.RunT(t)
	cacheStore, err := cache.New(ctx, cache.Config{Addr: redis.Addr()})
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	t.Cleanup(func() { _ = cacheStore.Close() })

	log := zerolog.New(logs)
	svc := usecase.NewUserService(sqlite.NewUserRepository(store), cacheStore, log, 0)
	return httpapi.NewServer(svc, store, cacheStore, log).Router()
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUserLifecycle(t *testing.T) {
	h := newRouter(t, io.Discard)

	rec := do(t, h, http.MethodPost, "/v1/users", `{"email":"Ada@Example.com","display_name":"Ada"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body)
	}
	var created struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Email != "ada@example.com" {
		t.Errorf("email = %q, want normalised", created.Email)
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/users/"+created.ID {
		t.Errorf("Location = %q, want /v1/users/%s", loc, created.ID)
	}

	rec = do(t, h, http.MethodPatch, "/v1/users/"+created.ID, `{"status":"suspended"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", rec.Code, rec.Body)
	}
	var patched struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Status != "suspended" {
		t.Errorf("status = %q, want suspended", patched.Status)
	}

	rec = do(t, h, http.MethodGet, "/v1/users?status=suspended", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body)
	}
	var list struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 {
		t.Errorf("total = %d, want 1", list.Total)
	}

	if rec := do(t, h, http.MethodDelete, "/v1/users/"+created.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/v1/users/"+created.ID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want 404", rec.Code)
	}
}

func TestErrorMapping(t *testing.T) {
	h := newRouter(t, io.Discard)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"malformed json", http.MethodPost, "/v1/users", `{"email":`, http.StatusBadRequest},
		{"invalid email", http.MethodPost, "/v1/users", `{"email":"nope","display_name":"X"}`, http.StatusBadRequest},
		{"missing body field", http.MethodPost, "/v1/users", `{"email":"x@example.com"}`, http.StatusBadRequest},
		{"bad uuid", http.MethodGet, "/v1/users/abc", "", http.StatusBadRequest},
		{"unknown user", http.MethodGet, "/v1/users/0193f3a0-0000-7000-8000-000000000000", "", http.StatusNotFound},
		{"no patch fields", http.MethodPatch, "/v1/users/0193f3a0-0000-7000-8000-000000000000", `{}`, http.StatusBadRequest},
		{"bad limit", http.MethodGet, "/v1/users?limit=abc", "", http.StatusBadRequest},
		{"bad status filter", http.MethodGet, "/v1/users?status=zombie", "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.path, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

func TestDuplicateEmailConflict(t *testing.T) {
	h := newRouter(t, io.Discard)
	body := `{"email":"dup@example.com","display_name":"Dup"}`
	if rec := do(t, h, http.MethodPost, "/v1/users", body); rec.Code != http.StatusCreated {
		t.Fatalf("first create status = %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/v1/users", body); rec.Code != http.StatusConflict {
		t.Errorf("second create status = %d, want 409", rec.Code)
	}
}

func TestHealthReportsDegradation(t *testing.T) {
	log := zerolog.Nop()
	svc := usecase.NewUserService(nil, nil, log, 0)

	rec := do(t, httpapi.NewServer(svc, healthStub{}, healthStub{}, log).Router(), http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Errorf("healthy status = %d, want 200", rec.Code)
	}

	failing := healthStub{err: context.DeadlineExceeded}
	rec = do(t, httpapi.NewServer(svc, failing, healthStub{}, log).Router(), http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("degraded status = %d, want 503", rec.Code)
	}
}

// The correlation id must be echoed, and every line for a request — the
// handler line included — must carry it so logs are joinable.
func TestRequestIDPropagatesIntoLogs(t *testing.T) {
	var buf bytes.Buffer
	h := newRouter(t, &buf)

	req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewBufferString(`{"email":"log@example.com","display_name":"L"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "trace-42")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "trace-42" {
		t.Fatalf("X-Request-ID = %q, want the inbound value echoed", got)
	}

	var lines []map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal(raw, &line); err != nil {
			t.Fatalf("log line is not JSON: %s", raw)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		t.Fatal("no log lines captured")
	}

	var sawUsecase, sawHTTP bool
	for _, line := range lines {
		if line["request_id"] != "trace-42" {
			t.Errorf("log line missing correlation id: %v", line)
		}
		switch line["layer"] {
		case "usecase":
			sawUsecase = true
		case "http":
			sawHTTP = true
		}
	}
	if !sawUsecase {
		t.Error("no usecase log line; the service must log through the request context")
	}
	if !sawHTTP {
		t.Error("no http log line")
	}
}

// A freshly minted id is echoed when the caller supplies none.
func TestRequestIDGeneratedWhenAbsent(t *testing.T) {
	h := newRouter(t, io.Discard)
	rec := do(t, h, http.MethodGet, "/healthz", "")
	if got := rec.Header().Get("X-Request-ID"); got == "" {
		t.Error("X-Request-ID not set on the response")
	}
}

// Structured log lines must not repeat keys: a duplicate layer field means a
// pooled logger inherits another component's identity and the record becomes
// ambiguous in a log aggregator.
func TestLogLinesHaveNoDuplicateKeys(t *testing.T) {
	var buf bytes.Buffer
	h := newRouter(t, &buf)

	rec := do(t, h, http.MethodPost, "/v1/users", `{"email":"dupkey@example.com","display_name":"D"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rec.Code)
	}
	do(t, h, http.MethodGet, "/v1/users/"+createdID(t, rec), "")

	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal(raw, &line); err != nil {
			t.Fatalf("invalid log line: %s", raw)
		}
		// encoding/json silently keeps the last duplicate, so detect repeats
		// by comparing field counts from a token-level scan.
		if n, want := countTopLevelKeys(raw), len(line); n != want {
			t.Errorf("duplicate keys in log line (%d occurrences, %d unique): %s", n, want, raw)
		}
		if line["layer"] == nil {
			t.Errorf("log line without layer: %s", raw)
		}
	}
}

func createdID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.ID
}

// countTopLevelKeys counts the string keys in a flat JSON object.
func countTopLevelKeys(raw []byte) int {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if _, err := dec.Token(); err != nil { // opening brace
		return 0
	}
	var n int
	for dec.More() {
		if _, err := dec.Token(); err != nil { // key
			return n
		}
		var skip any
		if err := dec.Decode(&skip); err != nil {
			return n
		}
		n++
	}
	return n
}
