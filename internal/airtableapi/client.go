// Package airtableapi is a minimal client for the parts of the Airtable REST
// API this function uses: listing records and managing a webhook.
package airtableapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.airtable.com"

type Client struct {
	APIKey  string
	BaseID  string
	BaseURL string // defaults to DefaultBaseURL; overridden in tests
	HTTP    *http.Client
}

func New(apiKey, baseID string) *Client {
	return &Client{
		APIKey:  apiKey,
		BaseID:  baseID,
		BaseURL: DefaultBaseURL,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Site is an active record from the table.
type Site struct {
	Name string `firestore:"name" json:"name"`
	URL  string `firestore:"url" json:"url"`
}

// FetchActiveSites pages through every record in the table and returns those
// with Active == true and a non-empty URL.
func (c *Client) FetchActiveSites(ctx context.Context, tableID string) ([]Site, error) {
	var sites []Site
	offset := ""
	for {
		q := url.Values{}
		if offset != "" {
			q.Set("offset", offset)
		}
		var page struct {
			Records []struct {
				Fields map[string]any `json:"fields"`
			} `json:"records"`
			Offset string `json:"offset"`
		}
		path := fmt.Sprintf("/v0/%s/%s", url.PathEscape(c.BaseID), url.PathEscape(tableID))
		if err := c.do(ctx, http.MethodGet, path, q, nil, &page); err != nil {
			return nil, err
		}
		for _, rec := range page.Records {
			u, _ := rec.Fields["URL"].(string)
			active, _ := rec.Fields["Active"].(bool)
			if u == "" || !active {
				continue
			}
			name, _ := rec.Fields["Name"].(string)
			sites = append(sites, Site{Name: name, URL: u})
		}
		if page.Offset == "" {
			return sites, nil
		}
		offset = page.Offset
	}
}

// Webhook is the response from creating a webhook.
type Webhook struct {
	ID              string `json:"id"`
	MACSecretBase64 string `json:"macSecretBase64"`
	ExpirationTime  string `json:"expirationTime"`
}

// CreateWebhook registers a webhook that fires on data or field changes in tableID.
func (c *Client) CreateWebhook(ctx context.Context, notificationURL, tableID string) (*Webhook, error) {
	body := map[string]any{
		"notificationUrl": notificationURL,
		"specification": map[string]any{
			"options": map[string]any{
				"filters": map[string]any{
					"dataTypes":         []string{"tableData", "tableFields"},
					"recordChangeScope": tableID,
				},
			},
		},
	}
	var wh Webhook
	path := fmt.Sprintf("/v0/bases/%s/webhooks", url.PathEscape(c.BaseID))
	if err := c.do(ctx, http.MethodPost, path, nil, body, &wh); err != nil {
		return nil, err
	}
	return &wh, nil
}

// RefreshWebhook extends the webhook's 7-day expiry.
func (c *Client) RefreshWebhook(ctx context.Context, webhookID string) error {
	path := fmt.Sprintf("/v0/bases/%s/webhooks/%s/refresh", url.PathEscape(c.BaseID), url.PathEscape(webhookID))
	return c.do(ctx, http.MethodPost, path, nil, nil, nil)
}

// DrainPayloads reads every pending payload starting at cursor and returns the
// cursor to resume from next time. The payload contents are discarded: any
// change means the whole table is re-read. Reading payloads also extends the
// webhook's expiry.
func (c *Client) DrainPayloads(ctx context.Context, webhookID string, cursor int) (int, error) {
	path := fmt.Sprintf("/v0/bases/%s/webhooks/%s/payloads", url.PathEscape(c.BaseID), url.PathEscape(webhookID))
	for range 50 { // bound the loop; leftovers are picked up on the next ping
		q := url.Values{}
		if cursor > 0 {
			q.Set("cursor", strconv.Itoa(cursor))
		}
		var resp struct {
			Cursor        int  `json:"cursor"`
			MightHaveMore bool `json:"mightHaveMore"`
		}
		if err := c.do(ctx, http.MethodGet, path, q, nil, &resp); err != nil {
			return cursor, err
		}
		cursor = resp.Cursor
		if !resp.MightHaveMore {
			break
		}
	}
	return cursor, nil
}

// VerifyMAC checks an X-Airtable-Content-MAC header against the request body.
func VerifyMAC(macSecretBase64 string, body []byte, header string) bool {
	secret, err := base64.StdEncoding.DecodeString(macSecretBase64)
	if err != nil || len(secret) == 0 {
		return false
	}
	got, ok := strings.CutPrefix(header, "hmac-sha256=")
	if !ok {
		return false
	}
	gotBytes, err := hex.DecodeString(got)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(gotBytes, mac.Sum(nil))
}

// Error is a non-2xx response from Airtable.
type Error struct {
	Status int
	Body   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("airtable: HTTP %d: %s", e.Status, e.Body)
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, in, out any) error {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return &Error{Status: resp.StatusCode, Body: string(b)}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
