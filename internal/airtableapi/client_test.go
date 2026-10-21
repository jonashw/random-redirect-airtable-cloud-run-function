package airtableapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchActiveSitesPagesAndFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("missing auth header")
		}
		switch r.URL.Query().Get("offset") {
		case "":
			fmt.Fprint(w, `{"records":[
				{"fields":{"Name":"a","URL":"https://a","Active":true}},
				{"fields":{"Name":"b","URL":"https://b"}},
				{"fields":{"Name":"c","Active":true}}
			],"offset":"p2"}`)
		case "p2":
			fmt.Fprint(w, `{"records":[{"fields":{"Name":"d","URL":"https://d","Active":true}}]}`)
		default:
			t.Errorf("unexpected offset %q", r.URL.Query().Get("offset"))
		}
	}))
	defer srv.Close()

	c := New("key", "app1")
	c.BaseURL = srv.URL
	sites, err := c.FetchActiveSites(context.Background(), "tbl1")
	if err != nil {
		t.Fatal(err)
	}
	want := []Site{{"a", "https://a"}, {"d", "https://d"}}
	if fmt.Sprint(sites) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", sites, want)
	}
}

func TestFetchActiveSitesReturnsRateLimitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errors":[{"error":"RATE_LIMIT_REACHED"}]}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := New("key", "app1")
	c.BaseURL = srv.URL
	_, err := c.FetchActiveSites(context.Background(), "tbl1")
	if e, ok := err.(*Error); !ok || e.Status != http.StatusTooManyRequests {
		t.Fatalf("got %v, want 429 *Error", err)
	}
}

func TestDrainPayloadsFollowsCursor(t *testing.T) {
	var cursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/bases/app1/webhooks/ach1/payloads" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		c := r.URL.Query().Get("cursor")
		cursors = append(cursors, c)
		if c == "5" {
			fmt.Fprint(w, `{"cursor":8,"mightHaveMore":true,"payloads":[]}`)
		} else {
			fmt.Fprint(w, `{"cursor":9,"mightHaveMore":false,"payloads":[]}`)
		}
	}))
	defer srv.Close()
	c := New("key", "app1")
	c.BaseURL = srv.URL
	next, err := c.DrainPayloads(context.Background(), "ach1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if next != 9 || fmt.Sprint(cursors) != "[5 8]" {
		t.Fatalf("next=%d cursors=%v", next, cursors)
	}
}

func TestVerifyMAC(t *testing.T) {
	secret := []byte("0123456789abcdef")
	secretB64 := base64.StdEncoding.EncodeToString(secret)
	body := []byte(`{"webhook":{"id":"ach1"}}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	good := "hmac-sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !VerifyMAC(secretB64, body, good) {
		t.Error("valid MAC rejected")
	}
	for _, h := range []string{"", "hmac-sha256=00", good[len("hmac-sha256="):]} {
		if VerifyMAC(secretB64, body, h) {
			t.Errorf("invalid MAC %q accepted", h)
		}
	}
	if VerifyMAC(secretB64, []byte(`{}`), good) {
		t.Error("MAC for a different body accepted")
	}
	if VerifyMAC("", body, good) {
		t.Error("empty secret accepted")
	}
}

func TestRequestsArePaced(t *testing.T) {
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		times = append(times, time.Now())
		if r.URL.Query().Get("offset") == "" {
			fmt.Fprint(w, `{"records":[],"offset":"p2"}`)
		} else {
			fmt.Fprint(w, `{"records":[]}`)
		}
	}))
	defer srv.Close()
	c := New("key", "app1")
	c.BaseURL = srv.URL
	c.MinInterval = 50 * time.Millisecond
	if _, err := c.FetchActiveSites(context.Background(), "tbl1"); err != nil {
		t.Fatal(err)
	}
	if len(times) != 2 || times[1].Sub(times[0]) < 45*time.Millisecond {
		t.Fatalf("requests not paced: %v", times)
	}
}
