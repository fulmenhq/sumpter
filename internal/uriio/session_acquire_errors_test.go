package uriio

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A successful HEAD is no guarantee that the subsequent GET is readable.
func TestAcquirePreservesUnavailableGETAfterHEAD(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		status     int
		want       error
	}{
		{"missing", "NoSuchKey", http.StatusNotFound, ErrObjectNotFound},
		{"denied", "AccessDenied", http.StatusForbidden, ErrObjectAccessDenied},
	} {
		for _, bounded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bounded=%v", tc.name, bounded), func(t *testing.T) {
				var heads, gets atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.Method {
					case http.MethodHead:
						heads.Add(1)
						w.Header().Set("Content-Length", "1")
						w.WriteHeader(http.StatusOK)
					case http.MethodGet:
						gets.Add(1)
						w.Header().Set("Content-Type", "application/xml")
						w.WriteHeader(tc.status)
						_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code><Message>synthetic-secret-value</Message></Error>", tc.code)
					default:
						w.WriteHeader(http.StatusMethodNotAllowed)
					}
				}))
				defer server.Close()
				cfg := &CredentialsConfig{Handles: map[string]HandleConfig{"test": {
					Region: "us-east-1", Endpoint: server.URL, ForcePathStyle: true, Insecure: true,
					AccessKeyID: Secret("synthetic-key"), SecretAccessKey: Secret("synthetic-secret-value"),
				}}}
				session := NewSession(NewResolver(cfg, nil), t.TempDir(), "get-error")
				defer func() {
					if err := session.Close(); err != nil {
						t.Error(err)
					}
				}()
				if bounded {
					session.SetStagingBudget(NewStagingBudget(StagingBudgetConfig{MaxBytes: 16, MaxFiles: 1, ObjectMax: 16}))
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				src, err := session.AcquireBoundedDeclared(ctx, "s3://bucket/item.xml", "test", 16, 1)
				if src != nil || !errors.Is(err, tc.want) {
					t.Fatalf("source=%v error=%v, want typed %v", src, err, tc.want)
				}
				if heads.Load() != 1 || gets.Load() != 1 {
					t.Fatalf("requests HEAD=%d GET=%d, want one each", heads.Load(), gets.Load())
				}
				if strings.Contains(err.Error(), "synthetic-secret-value") {
					t.Fatalf("raw error escaped redaction: %v", err)
				}
				if bounded {
					session.budget.mu.Lock()
					bytes, files := session.budget.usedBytes, session.budget.usedFiles
					session.budget.mu.Unlock()
					if bytes != 0 || files != 0 {
						t.Fatalf("failed GET retained reservation: bytes=%d files=%d", bytes, files)
					}
				}
			})
		}
	}
}
