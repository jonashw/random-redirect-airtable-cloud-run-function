Cloud Run function that redirects to a random URL from an Airtable base.

Expected table definition:

```
{
  Name: string,
  URL: string,
  Active: bool
}
```

Only records with `Active:true` are considered.

## Setup

Create a `.env` file:

```
AIRTABLE_API_KEY=patXXXXXX
AIRTABLE_BASE_ID=appXXXXXX
AIRTABLE_TABLE_ID=tblXXXXXX
FUNCTION_TARGET=RandomRedirect
```

## Dev

```sh
./dev.sh
```

Visit `http://localhost:8080`. Add `?debug` to list all active records instead of redirecting.
