# Wanderwell

Wanderwell is a tool for exploring your spatial activities. The name is inspired
by [Aloha Wanderwell](https://en.wikipedia.org/wiki/Aloha_Wanderwell), a
Canadian explorer, author, filmmaker, and aviator.

This repository is the consolidation of a bunch of tools that I have written in
the past with similar intents.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `STRAVA_CLIENT_ID` | Yes | Strava API client ID |
| `STRAVA_CLIENT_SECRET` | Yes | Strava API client secret |
| `REDIRECT_URI` | Yes | OAuth redirect URI |
| `WEBHOOK_URI` | Yes | Strava webhook callback URL |
| `VERIFY_TOKEN` | Yes | Token for webhook verification |
| `DATABASE_PATH` | Yes | PostgreSQL connection string |
| `SERVER_PORT` | Yes | Backend listen address (e.g., `:3000`) |
| `FRONTEND_URL` | Yes | Frontend URL for CORS |
| `SESSION_SECRET` | Yes | Session encryption secret |
| `SESSION_KEY` | Yes | Session key name |
| `TILE_CACHE_URL` | No | URL of the tile cache proxy for invalidation |
| `ADMIN_USER_ID` | No | Strava user ID for admin access (required for `/update` endpoint) |
| `RASTER_TILE_TOKEN` | Yes, when using raster explorer tiles | Existing database-backed user token used by `RequireTokenAuth` for the style-bundle job |

## Setup

### Setup Strava Callback

Wanderwell uses Strava webhook subscriptions to get notified of new or updated
activities. You can see it as a "push" version of the Strava API. More details
in the [Strava API documentation](https://developers.strava.com/docs/webhooks/).

To register a callback, you can use the following `curl` command. Make sure to
replace the `client_id`, `client_secret`, and `callback_url` with your own
values or source them from your `.env` file:

> [!NOTE]
> The backend server must be running and accessible at the specified
> `callback_url` for the registration to succeed. The Strava API will send a
> verification request to the `callback_url` during the registration process, and
> it must respond correctly to confirm the subscription.

> [!NOTE]
> Only one callback can be registered at a time.

```sh
set -o allexport
source .env
set +o allexport

curl -X POST https://www.strava.com/api/v3/push_subscriptions \
   -F client_id=$STRAVA_CLIENT_ID \
   -F client_secret=$STRAVA_CLIENT_SECRET \
   -F callback_url=$WEBHOOK_URI \
   -F verify_token=$VERIFY_TOKEN
```

To view the registered callback, you can use the following `curl` command:

```sh
set -o allexport
source .env
set +o allexport

curl -G https://www.strava.com/api/v3/push_subscriptions \
  -d client_id=$STRAVA_CLIENT_ID \
  -d client_secret=$STRAVA_CLIENT_SECRET
```

## Raster TileServer GL styles

TileServer GL loads styles from files at startup. The `tileserver-style-bundle`
Compose job fetches an internally-authenticated, generated bundle from the
backend and writes it to the `tileserver-data` volume before
`raster-tileserver` starts. The backend generates one `explorer-<athlete-id>`
style for every athlete currently in the database.

Set `RASTER_TILE_TOKEN` in `.env` to a valid `user_token.token` value. The
bundle job authenticates with the existing `RequireTokenAuth` middleware. To
include athletes added since the last startup, recreate the one-shot bundle job
and raster server:

```sh
docker compose up --force-recreate tileserver-style-bundle raster-tileserver
```

The resulting raster endpoint is:

```text
/styles/explorer-<athlete-id>/512/{z}/{x}/{y}.png
```

## Dev

### Connect to the database

```sh
docker compose exec postgis psql -U postgres -d wanderwell
```
