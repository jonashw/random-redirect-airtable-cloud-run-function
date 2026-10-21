// Command seedsnapshot fills the Firestore snapshot from a CSV export of the
// Airtable table, without making any Airtable API calls. Export the table
// from the Airtable UI (view menu → Download CSV), then:
//
//	go run ./cmd/seedsnapshot table.csv
//
// It uses your gcloud application-default credentials and project
// (gcloud auth application-default login; GOOGLE_CLOUD_PROJECT=<project>).
package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"strings"

	function "github.com/jwilson4/go-tshirt"
	"github.com/jwilson4/go-tshirt/internal/airtableapi"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: seedsnapshot table.csv")
		os.Exit(2)
	}
	sites, err := readSites(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := function.SeedSnapshot(context.Background(), sites); err != nil {
		fmt.Fprintf(os.Stderr, "save snapshot: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Seeded snapshot with %d active sites:\n", len(sites))
	for _, s := range sites {
		fmt.Printf("  %s  %s\n", s.Name, s.URL)
	}
}

// readSites reads Name, URL and Active columns, keeping rows with a URL and a
// ticked Active checkbox (exported by Airtable as "checked").
func readSites(path string) ([]airtableapi.Site, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}
	for _, name := range []string{"Name", "URL", "Active"} {
		if _, ok := col[name]; !ok {
			return nil, fmt.Errorf("%s has no %q column (found %v)", path, name, rows[0])
		}
	}
	sites := []airtableapi.Site{}
	for _, row := range rows[1:] {
		get := func(name string) string {
			if i := col[name]; i < len(row) {
				return strings.TrimSpace(row[i])
			}
			return ""
		}
		url := get("URL")
		switch strings.ToLower(get("Active")) {
		case "checked", "true", "yes", "1":
		default:
			continue
		}
		if url == "" {
			continue
		}
		sites = append(sites, airtableapi.Site{Name: get("Name"), URL: url})
	}
	return sites, nil
}
