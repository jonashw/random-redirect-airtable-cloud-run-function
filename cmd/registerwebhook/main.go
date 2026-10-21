// Command registerwebhook registers the Airtable webhook that pings the
// deployed function whenever the table changes. Run it once:
//
//	env $(cat .env | xargs) go run ./cmd/registerwebhook https://<function-url>/airtable-webhook
//
// AIRTABLE_API_KEY must be a token with the webhook:manage scope.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jwilson4/go-tshirt/internal/airtableapi"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: registerwebhook https://<function-url>/airtable-webhook")
		os.Exit(2)
	}
	apiKey := os.Getenv("AIRTABLE_API_KEY")
	baseID := os.Getenv("AIRTABLE_BASE_ID")
	tableID := os.Getenv("AIRTABLE_TABLE_ID")
	if apiKey == "" || baseID == "" || tableID == "" {
		fmt.Fprintln(os.Stderr, "AIRTABLE_API_KEY, AIRTABLE_BASE_ID and AIRTABLE_TABLE_ID must be set")
		os.Exit(2)
	}

	wh, err := airtableapi.New(apiKey, baseID).CreateWebhook(context.Background(), os.Args[1], tableID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create webhook: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Webhook created (expires %s unless refreshed).\n\n", wh.ExpirationTime)
	fmt.Println("Store these in the placeholder secrets, then redeploy:")
	fmt.Printf("  printf '%%s' '%s' | gcloud secrets versions add AIRTABLE_WEBHOOK_ID --data-file=-\n", wh.ID)
	fmt.Printf("  printf '%%s' '%s' | gcloud secrets versions add AIRTABLE_WEBHOOK_MAC_SECRET --data-file=-\n", wh.MACSecretBase64)
}
