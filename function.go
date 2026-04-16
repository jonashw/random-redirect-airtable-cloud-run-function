package function

import (
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"os"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/mehanizm/airtable"
)

func init() {
	functions.HTTP("RandomRedirect", randomRedirect)
}

func randomRedirect(w http.ResponseWriter, r *http.Request) {
	log.Printf("request received: %s %s from %s (UA: %s)", r.Method, r.URL.String(), r.RemoteAddr, r.UserAgent())

	apiKey := os.Getenv("AIRTABLE_API_KEY")
	baseID := os.Getenv("AIRTABLE_BASE_ID")
	tableId := os.Getenv("AIRTABLE_TABLE_ID")

	if apiKey == "" || baseID == "" || tableId == "" {
		http.Error(w, "missing required environment variables", http.StatusInternalServerError)
		return
	}

	client := airtable.NewClient(apiKey)
	table := client.GetTable(baseID, tableId)

	records, err := table.GetRecords().Do()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to fetch records: %v", err), http.StatusInternalServerError)
		return
	}

	type site struct {
		Name string
		URL  string
	}

	var sites []site
	for _, rec := range records.Records {
		url, _ := rec.Fields["URL"].(string)
		active, _ := rec.Fields["Active"].(bool)
		if url == "" || !active {
			continue
		}
		name, _ := rec.Fields["Name"].(string)
		sites = append(sites, site{Name: name, URL: url})
	}

	if len(sites) == 0 {
		http.Error(w, "no active records found", http.StatusNotFound)
		return
	}

	debug := r.URL.Query().Has("debug")

	if debug {
		w.Header().Set("Content-Type", "text/html")
		for _, s := range sites {
			fmt.Fprintf(w, "<div><a href=\"%s\">%s</a></div>\n", s.URL, s.Name)
		}
		return
	}

	chosen := sites[rand.IntN(len(sites))]
	log.Printf("redirecting to: %s (%s)", chosen.Name, chosen.URL)
	http.Redirect(w, r, chosen.URL, http.StatusFound)
}
