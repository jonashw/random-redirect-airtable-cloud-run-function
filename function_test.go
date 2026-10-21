package function

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeSource struct {
	sites []site
	err   error
	reads int
}

func (f *fakeSource) activeSites(context.Context) ([]site, error) {
	f.reads++
	return f.sites, f.err
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestApp(src sitesSource) (*app, *clock) {
	c := &clock{t: time.Unix(0, 0)}
	return &app{source: src, ttl: 10 * time.Second, now: c.now}, c
}

func scan(t *testing.T, a *app) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec
}

func TestCachesWithinTTLAndRereadsAfter(t *testing.T) {
	src := &fakeSource{sites: []site{{"one", "https://one.example"}}}
	a, c := newTestApp(src)

	for range 50 {
		if rec := scan(t, a); rec.Header().Get("Location") != "https://one.example" {
			t.Fatalf("status %d, location %q", rec.Code, rec.Header().Get("Location"))
		}
	}
	if src.reads != 1 {
		t.Fatalf("%d sheet reads for 50 scans within the TTL; want 1", src.reads)
	}

	src.sites = []site{{"two", "https://two.example"}}
	c.t = c.t.Add(10 * time.Second)
	if got := scan(t, a).Header().Get("Location"); got != "https://two.example" {
		t.Fatalf("after TTL, redirected to %q", got)
	}
	if src.reads != 2 {
		t.Fatalf("%d sheet reads; want 2", src.reads)
	}
}

func TestServesLastGoodListWhenSheetReadFails(t *testing.T) {
	src := &fakeSource{sites: []site{{"one", "https://one.example"}}}
	a, c := newTestApp(src)
	scan(t, a)

	src.sites, src.err = nil, errors.New("quota exceeded")
	c.t = c.t.Add(time.Minute)
	if got := scan(t, a).Header().Get("Location"); got != "https://one.example" {
		t.Fatalf("redirected to %q", got)
	}
}

func TestErrorWithNothingCached(t *testing.T) {
	a, _ := newTestApp(&fakeSource{err: errors.New("boom")})
	if rec := scan(t, a); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestEmptyListIsCachedAnd404s(t *testing.T) {
	src := &fakeSource{sites: []site{}}
	a, _ := newTestApp(src)
	for range 3 {
		if rec := scan(t, a); rec.Code != http.StatusNotFound {
			t.Fatalf("status %d", rec.Code)
		}
	}
	if src.reads != 1 {
		t.Fatalf("%d reads; want 1", src.reads)
	}
}

func TestParseRows(t *testing.T) {
	rows := [][]any{
		{"Active", "Name", "URL", "Notes"},
		{true, "a", "https://a"},
		{false, "b", "https://b"},
		{"checked", "c", "https://c"},
		{true, "d"},
		{"TRUE", "e", " https://e "},
		{},
	}
	sites, err := parseRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(sites); got != "[{a https://a} {c https://c} {e https://e}]" {
		t.Fatalf("got %s", got)
	}
	if _, err := parseRows([][]any{{"Name", "URL"}}); err == nil {
		t.Fatal("want error for missing Active column")
	}
}

func TestSheetReaderRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/spreadsheets/sheet1/values/Sites!A:C" || r.URL.Query().Get("valueRenderOption") != "UNFORMATTED_VALUE" {
			t.Errorf("unexpected request %s", r.URL)
		}
		fmt.Fprint(w, `{"values":[["Name","URL","Active"],["a","https://a",true]]}`)
	}))
	defer srv.Close()
	s := &sheetReader{baseURL: srv.URL, http: srv.Client(), sheetID: "sheet1", rng: "Sites!A:C"}
	sites, err := s.activeSites(context.Background())
	if err != nil || fmt.Sprint(sites) != "[{a https://a}]" {
		t.Fatalf("sites=%v err=%v", sites, err)
	}
}

func TestDebugPageIsATable(t *testing.T) {
	src := &fakeSource{sites: []site{
		{"one", "https://one.example"},
		{"<b>two</b>", "https://two.example/?a=1&b=2"},
	}}
	a, _ := newTestApp(src)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?debug", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{
		"<table>", "<th>Name</th>", "2 active sites",
		`<tr class="chosen">`,
		"&lt;b&gt;two&lt;/b&gt;", // names are escaped
		`href="https://two.example/?a=1&amp;b=2"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("debug page missing %q:\n%s", want, body)
		}
	}
	if n := strings.Count(body, `class="chosen"`); n != 1 {
		t.Errorf("%d highlighted rows; want 1", n)
	}
}
