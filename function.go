package function

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
)

// cacheTTL bounds how stale a redirect can be after an edit in the sheet. It
// also caps Sheets reads at one per TTL per instance, far under the
// 60 reads/minute per-user quota.
const cacheTTL = 10 * time.Second

// debugPage lists the active sites; the row picked for this request is highlighted.
var debugPage = template.Must(template.New("debug").Funcs(template.FuncMap{"inc": func(i int) int { return i + 1 }}).Parse(`<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Active sites</title>
<style>
  body { font: 15px/1.4 system-ui, sans-serif; margin: 16px; }
  table { border-collapse: collapse; width: 100%; }
  th, td { text-align: left; padding: 6px 10px; border-bottom: 1px solid #ddd; vertical-align: top; }
  td.url { word-break: break-all; }
  tr.chosen { background: #fff3c4; font-weight: 600; }
  caption { text-align: left; padding-bottom: 8px; color: #555; }
</style>
<table>
  <caption>{{len .Sites}} active sites. The highlighted row is this request's pick.</caption>
  <thead><tr><th>#</th><th>Name</th><th>URL</th></tr></thead>
  <tbody>
  {{- range $i, $s := .Sites}}
    <tr{{if eq $s $.Chosen}} class="chosen"{{end}}><td>{{inc $i}}</td><td>{{$s.Name}}</td><td class="url"><a href="{{$s.URL}}">{{$s.URL}}</a></td></tr>
  {{- end}}
  </tbody>
</table>
`))

func init() {
	functions.HTTP("RandomRedirect", handle)
}

var (
	appOnce sync.Once
	appInst *app
	appErr  error
)

func handle(w http.ResponseWriter, r *http.Request) {
	appOnce.Do(func() { appInst, appErr = newAppFromEnv(context.Background()) })
	if appErr != nil {
		log.Printf("startup error: %v", appErr)
		http.Error(w, appErr.Error(), http.StatusInternalServerError)
		return
	}
	appInst.ServeHTTP(w, r)
}

type sitesSource interface {
	activeSites(ctx context.Context) ([]site, error)
}

type app struct {
	source sitesSource
	ttl    time.Duration
	now    func() time.Time

	mu        sync.Mutex // held during a sheet read, so concurrent scans share it
	sites     []site
	fetchedAt time.Time
}

func newAppFromEnv(ctx context.Context) (*app, error) {
	sheetID := os.Getenv("SHEET_ID")
	if sheetID == "" {
		return nil, errors.New("missing required environment variable SHEET_ID")
	}
	rng := os.Getenv("SHEET_RANGE") // optional; defaults to the first tab
	if rng == "" {
		rng = "A:Z"
	}
	src, err := newSheetReader(ctx, sheetID, rng)
	if err != nil {
		return nil, fmt.Errorf("google credentials: %w", err)
	}
	return &app{source: src, ttl: cacheTTL, now: time.Now}, nil
}

// activeSites returns the cached list, re-reading the sheet once it is older
// than the TTL. If a re-read fails, it keeps serving the last good list.
func (a *app) activeSites(ctx context.Context) ([]site, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sites != nil && a.now().Sub(a.fetchedAt) < a.ttl {
		return a.sites, nil
	}
	sites, err := a.source.activeSites(ctx)
	if err != nil {
		if a.sites != nil {
			log.Printf("sheet read failed, serving cached list: %v", err)
			return a.sites, nil
		}
		return nil, err
	}
	a.sites, a.fetchedAt = sites, a.now()
	return sites, nil
}

func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("request received: %s %s from %s (UA: %s)", r.Method, r.URL.String(), r.RemoteAddr, r.UserAgent())

	sites, err := a.activeSites(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to fetch records: %v", err), http.StatusInternalServerError)
		return
	}
	if len(sites) == 0 {
		http.Error(w, "no active records found", http.StatusNotFound)
		return
	}

	chosen := sites[rand.IntN(len(sites))]

	if r.URL.Query().Has("debug") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := debugPage.Execute(w, map[string]any{"Sites": sites, "Chosen": chosen}); err != nil {
			log.Printf("debug page: %v", err)
		}
		return
	}

	log.Printf("redirecting to: %s (%s)", chosen.Name, chosen.URL)
	http.Redirect(w, r, chosen.URL, http.StatusFound)
}
