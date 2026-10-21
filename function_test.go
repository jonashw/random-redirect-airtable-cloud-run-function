package function

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jwilson4/go-tshirt/internal/airtableapi"
)

// fakeAirtable serves one page of records and counts record reads.
type fakeAirtable struct {
	srv       *httptest.Server
	reads     atomic.Int32
	refreshes atomic.Int32
	url       atomic.Value // current site URL
}

func newFakeAirtable(t *testing.T) *fakeAirtable {
	f := &fakeAirtable{}
	f.url.Store("https://one.example")
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v0/app1/tbl1":
			f.reads.Add(1)
			fmt.Fprintf(w, `{"records":[{"fields":{"Name":"one","URL":%q,"Active":true}}]}`, f.url.Load())
		case strings.HasSuffix(r.URL.Path, "/payloads"):
			fmt.Fprint(w, `{"cursor":3,"mightHaveMore":false,"payloads":[]}`)
		case strings.HasSuffix(r.URL.Path, "/refresh"):
			f.refreshes.Add(1)
			fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected Airtable call %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

const macSecret = "MDEyMzQ1Njc4OWFiY2RlZg==" // base64("0123456789abcdef")

func newTestApp(f *fakeAirtable, s store) *app {
	at := airtableapi.New("key", "app1")
	at.BaseURL = f.srv.URL
	at.MinInterval = 0
	return &app{
		store:            s,
		airtable:         at,
		tableID:          "tbl1",
		webhookID:        "ach1",
		webhookMACSecret: macSecret,
		refreshToken:     "tok",
	}
}

func scan(t *testing.T, a *app) string {
	t.Helper()
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("scan: status %d: %s", rec.Code, rec.Body)
	}
	return rec.Header().Get("Location")
}

func signedPing(body string) *http.Request {
	secret, _ := base64.StdEncoding.DecodeString(macSecret)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(body))
	r := httptest.NewRequest(http.MethodPost, "/airtable-webhook", strings.NewReader(body))
	r.Header.Set("X-Airtable-Content-MAC", "hmac-sha256="+hex.EncodeToString(mac.Sum(nil)))
	return r
}

func TestScansReadSnapshotNotAirtable(t *testing.T) {
	f := newFakeAirtable(t)
	a := newTestApp(f, &memoryStore{})

	for range 20 {
		if got := scan(t, a); got != "https://one.example" {
			t.Fatalf("redirected to %q", got)
		}
	}
	if n := f.reads.Load(); n != 1 {
		t.Fatalf("Airtable read %d times for 20 scans; want 1 (initial fill only)", n)
	}
}

func TestWebhookUpdatesSnapshot(t *testing.T) {
	f := newFakeAirtable(t)
	s := &memoryStore{}
	a := newTestApp(f, s)
	scan(t, a)

	f.url.Store("https://two.example")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, signedPing(`{"base":{"id":"app1"},"webhook":{"id":"ach1"}}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("webhook: status %d: %s", rec.Code, rec.Body)
	}
	if got := scan(t, a); got != "https://two.example" {
		t.Fatalf("after webhook, redirected to %q", got)
	}
	if snap, _ := s.Load(context.Background()); snap.WebhookCursor != 3 {
		t.Fatalf("cursor = %d, want 3", snap.WebhookCursor)
	}
}

func TestWebhookRejectsBadSignatureAndUnknownID(t *testing.T) {
	f := newFakeAirtable(t)
	a := newTestApp(f, &memoryStore{})

	r := httptest.NewRequest(http.MethodPost, "/airtable-webhook", strings.NewReader(`{"webhook":{"id":"ach1"}}`))
	r.Header.Set("X-Airtable-Content-MAC", "hmac-sha256=00")
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad MAC: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	a.ServeHTTP(rec, signedPing(`{"webhook":{"id":"other"}}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown webhook: status %d", rec.Code)
	}
	if f.reads.Load() != 0 {
		t.Fatal("rejected pings must not read Airtable")
	}
}

func TestRefreshEndpoint(t *testing.T) {
	f := newFakeAirtable(t)
	a := newTestApp(f, &memoryStore{})

	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/refresh", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status %d", rec.Code)
	}

	r := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	r.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	a.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: status %d: %s", rec.Code, rec.Body)
	}
	if f.reads.Load() != 1 || f.refreshes.Load() != 1 {
		t.Fatalf("reads=%d webhook refreshes=%d, want 1 and 1", f.reads.Load(), f.refreshes.Load())
	}
}

// failingStore loads once, then fails like an unreachable Firestore.
type failingStore struct {
	memoryStore
	fail atomic.Bool
}

func (s *failingStore) Load(ctx context.Context) (*snapshot, error) {
	if s.fail.Load() {
		return nil, errors.New("unavailable")
	}
	return s.memoryStore.Load(ctx)
}

func TestStoreOutageServesLastGood(t *testing.T) {
	f := newFakeAirtable(t)
	s := &failingStore{}
	a := newTestApp(f, s)
	scan(t, a)
	s.fail.Store(true)

	for range 5 {
		if got := scan(t, a); got != "https://one.example" {
			t.Fatalf("redirected to %q", got)
		}
	}
	if n := f.reads.Load(); n != 1 {
		t.Fatalf("Airtable read %d times; want 1", n)
	}
}

func TestOlderRefreshDoesNotOverwriteNewer(t *testing.T) {
	s := &memoryStore{}
	now := time.Now()
	ctx := context.Background()
	s.SaveSites(ctx, []airtableapi.Site{{Name: "new"}}, now)
	s.SaveSites(ctx, []airtableapi.Site{{Name: "old"}}, now.Add(-time.Second))
	snap, _ := s.Load(ctx)
	if snap.Sites[0].Name != "new" {
		t.Fatalf("older refresh overwrote newer snapshot")
	}
}

func TestConcurrentColdScansCoalesce(t *testing.T) {
	f := newFakeAirtable(t)
	a := newTestApp(f, &failingStore{}) // store never has a snapshot to give
	a.store.(*failingStore).fail.Store(true)

	done := make(chan struct{})
	for range 20 {
		go func() {
			defer func() { done <- struct{}{} }()
			rec := httptest.NewRecorder()
			a.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		}()
	}
	for range 20 {
		<-done
	}
	if n := f.reads.Load(); n > 2 {
		t.Fatalf("20 concurrent cold scans caused %d Airtable reads; want at most 2", n)
	}
}

// saveFailingStore accepts reads but rejects every snapshot write.
type saveFailingStore struct{ memoryStore }

func (s *saveFailingStore) SaveSites(context.Context, []airtableapi.Site, time.Time) error {
	return errors.New("write rejected")
}

func TestSaveFailureIsReportedButScanStillServed(t *testing.T) {
	f := newFakeAirtable(t)
	a := newTestApp(f, &saveFailingStore{})

	if got := scan(t, a); got != "https://one.example" {
		t.Fatalf("redirected to %q", got)
	}

	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, signedPing(`{"webhook":{"id":"ach1"}}`))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("webhook with failed save: status %d, want 502", rec.Code)
	}

	r := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	r.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	a.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("refresh with failed save: status %d, want 502", rec.Code)
	}
}

func TestEmptyTableIsAValidFallback(t *testing.T) {
	f := newFakeAirtable(t)
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.reads.Add(1)
		fmt.Fprint(w, `{"records":[]}`)
	})
	s := &failingStore{}
	a := newTestApp(f, s)

	for i := range 3 {
		if i == 1 {
			s.fail.Store(true)
		}
		rec := httptest.NewRecorder()
		a.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("scan %d: status %d, want 404", i, rec.Code)
		}
	}
	if n := f.reads.Load(); n != 1 {
		t.Fatalf("Airtable read %d times; want 1", n)
	}
}
