package function

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"

	"github.com/jwilson4/go-tshirt/internal/airtableapi"
)

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

// app serves scans from a cached snapshot of the Airtable table. The snapshot
// is rewritten when Airtable's webhook pings /airtable-webhook, and on a
// schedule via /refresh, so scans never call Airtable themselves.
type app struct {
	store    store
	airtable *airtableapi.Client
	tableID  string

	webhookID        string // AIRTABLE_WEBHOOK_ID
	webhookMACSecret string // AIRTABLE_WEBHOOK_MAC_SECRET
	refreshToken     string // REFRESH_TOKEN, sent by Cloud Scheduler as a bearer token

	mu       sync.Mutex
	lastGood []airtableapi.Site // served if the store is unreachable

	refreshMu        sync.Mutex
	lastRefreshStart time.Time
	lastRefreshSites []airtableapi.Site
	lastRefreshErr   error
}

func newAppFromEnv(ctx context.Context) (*app, error) {
	apiKey := os.Getenv("AIRTABLE_API_KEY")
	baseID := os.Getenv("AIRTABLE_BASE_ID")
	tableID := os.Getenv("AIRTABLE_TABLE_ID")
	if apiKey == "" || baseID == "" || tableID == "" {
		return nil, errors.New("missing required environment variables")
	}

	var s store
	if os.Getenv("SNAPSHOT_STORE") == "memory" {
		s = &memoryStore{}
	} else {
		fs, err := newFirestoreStore(ctx)
		if err != nil {
			return nil, fmt.Errorf("firestore: %w", err)
		}
		s = fs
	}

	return &app{
		store:            s,
		airtable:         airtableapi.New(apiKey, baseID),
		tableID:          tableID,
		webhookID:        os.Getenv("AIRTABLE_WEBHOOK_ID"),
		webhookMACSecret: os.Getenv("AIRTABLE_WEBHOOK_MAC_SECRET"),
		refreshToken:     os.Getenv("REFRESH_TOKEN"),
	}, nil
}

func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/airtable-webhook":
		a.handleWebhook(w, r)
	case "/refresh":
		a.handleRefresh(w, r)
	default:
		a.handleRedirect(w, r)
	}
}

func (a *app) handleRedirect(w http.ResponseWriter, r *http.Request) {
	log.Printf("request received: %s %s from %s (UA: %s)", r.Method, r.URL.String(), r.RemoteAddr, r.UserAgent())

	sites, err := a.sites(r.Context())
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
		w.Header().Set("Content-Type", "text/html")
		for _, s := range sites {
			link := fmt.Sprintf("<a href=\"%s\">%s</a>", html.EscapeString(s.URL), html.EscapeString(s.Name))
			if s == chosen {
				link = "<strong>" + link + "</strong>"
			}
			fmt.Fprintf(w, "<div>%s</div>\n", link)
		}
		return
	}

	log.Printf("redirecting to: %s (%s)", chosen.Name, chosen.URL)
	http.Redirect(w, r, chosen.URL, http.StatusFound)
}

// sites returns the active sites from the snapshot. It only calls Airtable
// when no snapshot exists yet (first deploy), or when the store is down and
// this instance has nothing cached.
func (a *app) sites(ctx context.Context) ([]airtableapi.Site, error) {
	snap, err := a.store.Load(ctx)
	if err == nil {
		a.mu.Lock()
		a.lastGood = snap.Sites
		a.mu.Unlock()
		return snap.Sites, nil
	}
	if !errors.Is(err, errNoSnapshot) {
		log.Printf("snapshot load failed: %v", err)
		a.mu.Lock()
		last := a.lastGood
		a.mu.Unlock()
		if last != nil {
			return last, nil
		}
	}
	sites, err := a.refresh(ctx)
	if sites != nil {
		// Fetched fine; only saving it failed. Still good for this scan.
		return sites, nil
	}
	return nil, err
}

// refresh re-reads the table from Airtable and saves the snapshot. Calls on
// one instance are serialised, and a caller whose request is already covered
// by a refresh that started after it arrived reuses that result instead of
// calling Airtable again. This keeps a burst of webhook pings or cold scans
// down to one or two Airtable reads.
//
// If the fetch succeeds but saving the snapshot fails, refresh returns the
// fetched sites together with the save error, so the webhook and Scheduler
// handlers report failure instead of acknowledging an unapplied update.
func (a *app) refresh(ctx context.Context) ([]airtableapi.Site, error) {
	requested := time.Now()
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	if a.lastRefreshStart.After(requested) {
		return a.lastRefreshSites, a.lastRefreshErr
	}

	start := time.Now()
	sites, err := a.airtable.FetchActiveSites(ctx, a.tableID)
	if err == nil {
		a.mu.Lock()
		a.lastGood = sites
		a.mu.Unlock()
		if err = a.store.SaveSites(ctx, sites, start); err != nil {
			err = fmt.Errorf("snapshot save failed: %w", err)
			log.Print(err)
		} else {
			log.Printf("snapshot refreshed: %d active sites", len(sites))
		}
	}
	a.lastRefreshStart, a.lastRefreshSites, a.lastRefreshErr = start, sites, err
	return sites, err
}

func (a *app) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.webhookMACSecret == "" {
		http.Error(w, "webhook not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !airtableapi.VerifyMAC(a.webhookMACSecret, body, r.Header.Get("X-Airtable-Content-MAC")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	var ping struct {
		Webhook struct {
			ID string `json:"id"`
		} `json:"webhook"`
	}
	_ = json.Unmarshal(body, &ping)
	if a.webhookID != "" && ping.Webhook.ID != a.webhookID {
		http.Error(w, "unknown webhook", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	// Reading the payloads acknowledges them and extends the webhook's expiry.
	// Their contents don't matter: any change triggers a full re-read.
	cursor := 0
	if snap, _ := a.store.Load(ctx); snap != nil {
		cursor = snap.WebhookCursor
	}
	if next, err := a.airtable.DrainPayloads(ctx, ping.Webhook.ID, cursor); err != nil {
		log.Printf("webhook payload read failed: %v", err)
	} else if next != cursor {
		if err := a.store.SaveCursor(ctx, next); err != nil {
			log.Printf("webhook cursor save failed: %v", err)
		}
	}

	if _, err := a.refresh(ctx); err != nil {
		log.Printf("refresh after webhook failed: %v", err)
		http.Error(w, "refresh failed", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.refreshToken == "" {
		http.Error(w, "refresh not configured", http.StatusServiceUnavailable)
		return
	}
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(a.refreshToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	sites, err := a.refresh(ctx)
	if err != nil {
		log.Printf("scheduled refresh failed: %v", err)
		http.Error(w, "refresh failed", http.StatusBadGateway)
		return
	}
	if a.webhookID != "" {
		if err := a.airtable.RefreshWebhook(ctx, a.webhookID); err != nil {
			log.Printf("webhook expiry refresh failed: %v", err)
			http.Error(w, "webhook refresh failed", http.StatusBadGateway)
			return
		}
	}
	fmt.Fprintf(w, "refreshed: %d active sites\n", len(sites))
}
