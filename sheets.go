package function

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2/google"
)

const sheetsScope = "https://www.googleapis.com/auth/spreadsheets.readonly"

type site struct {
	Name string
	URL  string
}

// sheetReader reads the redirect list from a Google Sheet using the
// function's own identity (Application Default Credentials). The sheet must
// be shared with that identity.
type sheetReader struct {
	baseURL string // https://sheets.googleapis.com; overridden in tests
	http    *http.Client
	sheetID string
	rng     string // A1 range, e.g. "Sites" or "Sites!A:C"
}

func newSheetReader(ctx context.Context, sheetID, rng string) (*sheetReader, error) {
	client, err := google.DefaultClient(ctx, sheetsScope)
	if err != nil {
		return nil, err
	}
	return &sheetReader{baseURL: "https://sheets.googleapis.com", http: client, sheetID: sheetID, rng: rng}, nil
}

// activeSites returns rows with a URL and a ticked Active cell. Columns are
// found by their header (Name, URL, Active), so their order doesn't matter.
func (s *sheetReader) activeSites(ctx context.Context) ([]site, error) {
	u := fmt.Sprintf("%s/v4/spreadsheets/%s/values/%s?valueRenderOption=UNFORMATTED_VALUE",
		s.baseURL, url.PathEscape(s.sheetID), url.PathEscape(s.rng))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("sheets: HTTP %d: %s", resp.StatusCode, b)
	}
	var body struct {
		Values [][]any `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return parseRows(body.Values)
}

func parseRows(rows [][]any) ([]site, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("sheet is empty: expected a header row with Name, URL and Active")
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.TrimSpace(fmt.Sprint(h))] = i
	}
	for _, name := range []string{"Name", "URL", "Active"} {
		if _, ok := col[name]; !ok {
			return nil, fmt.Errorf("sheet header has no %q column (found %v)", name, rows[0])
		}
	}
	sites := []site{} // non-nil: an empty list is a valid result
	for _, row := range rows[1:] {
		cell := func(name string) any {
			if i := col[name]; i < len(row) {
				return row[i]
			}
			return nil
		}
		u := strings.TrimSpace(fmt.Sprint(cell("URL")))
		if cell("URL") == nil || u == "" || !isTicked(cell("Active")) {
			continue
		}
		name, _ := cell("Name").(string)
		sites = append(sites, site{Name: strings.TrimSpace(name), URL: u})
	}
	return sites, nil
}

// isTicked accepts a checkbox (true) or text such as "TRUE" or "checked"
// (how Airtable's CSV export writes checkboxes).
func isTicked(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "checked", "yes", "1":
			return true
		}
	}
	return false
}
