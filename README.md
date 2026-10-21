Cloud Run function that redirects to a random URL from a Google Sheet.

Expected sheet layout: a header row with these columns, in any order (extra
columns are ignored):

| Name | URL | Active |
|---|---|---|
| text | text | checkbox |

Only rows where `Active` is ticked and `URL` is non-empty are considered.
Edit the sheet from anywhere, including the Google Sheets Android app.

## How it works

Each instance keeps the list in memory for 10 seconds, then re-reads the
sheet, so edits show up within about 10 seconds. That's at most 6 Sheets reads
a minute per instance, well under the
[Sheets API quota](https://developers.google.com/workspace/sheets/api/limits)
of 60 reads a minute per user (the function's service account counts as one
user), and the Sheets API has no daily or monthly cap. If a read fails, the
instance keeps serving the last list it read.

The function reads the sheet with its own Google identity, so there's no API
key. See [the alternatives analysis](docs/alternatives-analysis/replace-airtable.md)
for why the list moved from Airtable to Google Sheets.

## Setup

1. Create the sheet. To move from Airtable, download the table as CSV
   (view menu → **Download CSV**) and use **File → Import** in Google Sheets.
   Then select the `Active` column and use **Insert → Checkbox**. Cells that
   read `checked` or `TRUE` are also accepted as ticked.
2. Share the sheet (Viewer) with the function's runtime service account and
   store the sheet ID (the long ID in the sheet's URL) as a secret:

   ```sh
   PROJECT=$(gcloud config get-value project)
   PROJECT_NUMBER=$(gcloud projects describe $PROJECT --format='value(projectNumber)')
   echo "Share the sheet with: ${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"

   gcloud services enable sheets.googleapis.com
   printf '%s' '<sheet-id>' | gcloud secrets create SHEET_ID --data-file=-
   # If secret access is granted per secret rather than project-wide:
   gcloud secrets add-iam-policy-binding SHEET_ID \
     --member="serviceAccount:${PROJECT_NUMBER}-compute@developer.gserviceaccount.com" \
     --role=roles/secretmanager.secretAccessor
   ```
3. Deploy (`cloudbuild.yaml`). The old `AIRTABLE_*` secrets are no longer used
   and can be deleted afterwards.

Optional: `SHEET_RANGE` picks a tab or range (e.g. `Sites` or `Sites!A:C`).
The default is the first tab.

## Dev

Create a `.env` file:

```
SHEET_ID=<sheet-id>
FUNCTION_TARGET=RandomRedirect
```

Log in so the function can read the sheet as you:

```sh
gcloud auth application-default login \
  --scopes=https://www.googleapis.com/auth/spreadsheets.readonly,https://www.googleapis.com/auth/cloud-platform
./dev.sh
```

Visit `http://localhost:8080`. Add `?debug` to list all active records instead of redirecting.
