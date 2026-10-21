Cloud Run function that redirects to a random URL from an Airtable base.

Expected table definition:

```
{
  Name: string,
  URL: string,
  Active: bool
}
```

Only records where `Active==true` are considered.

## How caching works

Scans never call Airtable. The active records live in one Firestore document
(`cache/sites`), and each scan reads that document. It is kept current two ways:

- **`POST /airtable-webhook`**: an Airtable webhook pings this route whenever
  the table changes. The function verifies the signature, re-reads the table
  and rewrites the snapshot, typically within a few seconds of the edit.
- **`POST /refresh`**: Cloud Scheduler calls this once a day. It re-syncs
  the snapshot in case a ping was missed, and extends the webhook's 7-day
  expiry.

If Firestore is unreachable, an instance serves the last snapshot it read.
See [the alternatives analysis](docs/alternatives-analysis/airtable-cache.md)
for why this design was chosen.

### Airtable API budget

Every Airtable API call counts toward the workspace's monthly cap (1,000 on
the Free plan, reset on the 1st). Scans cost nothing. Each edit costs about
2–3 calls (payload read + one call per 100 records), and the daily refresh
about 2. That leaves room for roughly 300 edits a month on Free. Don't make
the safety-net job hourly on Free: that alone would use ~720 calls a month.

## Setup

Create a `.env` file:

```
AIRTABLE_API_KEY=patXXXXXX
AIRTABLE_BASE_ID=appXXXXXX
AIRTABLE_TABLE_ID=tblXXXXXX
FUNCTION_TARGET=RandomRedirect
SNAPSHOT_STORE=memory
```

`SNAPSHOT_STORE=memory` keeps the snapshot in memory instead of Firestore,
for local development.

The Airtable token needs the `data.records:read` and `webhook:manage` scopes.

## One-time GCP and Airtable setup

Run these once before deploying the caching version. They assume
`gcloud config set project <project>` has been done.

```sh
PROJECT=$(gcloud config get-value project)
PROJECT_NUMBER=$(gcloud projects describe $PROJECT --format='value(projectNumber)')
SA="${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"  # the function's runtime identity
URL=$(gcloud functions describe random-redirect --region=us-west1 --gen2 --format='value(serviceConfig.uri)')

# 1. Firestore database for the snapshot
gcloud firestore databases create --location=us-west1 --type=firestore-native
gcloud projects add-iam-policy-binding $PROJECT --member="serviceAccount:$SA" --role=roles/datastore.user

# 2. Secrets. The webhook ones start as "-" placeholders (treated as unset)
#    so the function can deploy before the webhook is registered.
REFRESH_TOKEN=$(openssl rand -hex 32)
printf '%s' "$REFRESH_TOKEN" | gcloud secrets create REFRESH_TOKEN --data-file=-
printf '%s' '-' | gcloud secrets create AIRTABLE_WEBHOOK_ID --data-file=-
printf '%s' '-' | gcloud secrets create AIRTABLE_WEBHOOK_MAC_SECRET --data-file=-

# 3. If secret access is granted per secret rather than project-wide:
for s in REFRESH_TOKEN AIRTABLE_WEBHOOK_ID AIRTABLE_WEBHOOK_MAC_SECRET; do
  gcloud secrets add-iam-policy-binding $s --member="serviceAccount:$SA" --role=roles/secretmanager.secretAccessor
done
```

### Seed the cache without the Airtable API

If the monthly API cap is spent, fill the snapshot from a CSV export so scans
work right away: in Airtable, open the table's view menu → **Download CSV**,
then:

```sh
gcloud auth application-default login
GOOGLE_CLOUD_PROJECT=$PROJECT go run ./cmd/seedsnapshot ~/Downloads/<table>.csv
```

### Deploy, then register the webhook

Deploy as usual (`cloudbuild.yaml` mounts the three new secrets). Then, once
the Airtable API is available:

```sh
# Needs the webhook:manage scope on the token. Prints two
# `gcloud secrets versions add` commands; run them, then redeploy.
env $(cat .env | xargs) go run ./cmd/registerwebhook "$URL/airtable-webhook"

# Daily safety net
gcloud scheduler jobs create http airtable-cache-refresh \
  --location=us-west1 --schedule="0 9 * * *" \
  --uri="$URL/refresh" --http-method=POST \
  --headers="Authorization=Bearer $REFRESH_TOKEN"
```

To check it end to end: edit a record in Airtable, wait a few seconds, then
open `$URL?debug`. The function logs `snapshot refreshed: N active sites` on
each refresh.

## Dev

```sh
./dev.sh
```

Visit `http://localhost:8080`. Add `?debug` to list all active records instead of redirecting.
