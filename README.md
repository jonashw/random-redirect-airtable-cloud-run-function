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
- **`POST /refresh`**: Cloud Scheduler calls this hourly. It re-syncs the
  snapshot in case a ping was missed, and extends the webhook's 7-day expiry.

If Firestore is unreachable, an instance serves the last snapshot it read.
See [the alternatives analysis](docs/alternatives-analysis/airtable-cache.md)
for why this design was chosen.

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

# 2. Shared secret for the hourly refresh job
REFRESH_TOKEN=$(openssl rand -hex 32)
printf '%s' "$REFRESH_TOKEN" | gcloud secrets create REFRESH_TOKEN --data-file=-

# 3. Register the Airtable webhook. Add the webhook:manage scope to the token first.
#    This prints two more `gcloud secrets create` commands; run them.
env $(cat .env | xargs) go run ./cmd/registerwebhook "$URL/airtable-webhook"

# 4. If secret access is granted per secret rather than project-wide:
for s in REFRESH_TOKEN AIRTABLE_WEBHOOK_ID AIRTABLE_WEBHOOK_MAC_SECRET; do
  gcloud secrets add-iam-policy-binding $s --member="serviceAccount:$SA" --role=roles/secretmanager.secretAccessor
done
```

Then deploy (`cloudbuild.yaml` now mounts the three new secrets), and create
the hourly safety-net job:

```sh
gcloud scheduler jobs create http airtable-cache-refresh \
  --location=us-west1 --schedule="0 * * * *" \
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
