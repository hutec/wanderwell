# Wanderwell

Wanderwell is a tool for exploring your spatial activities. The name is inspired
by [Aloha Wanderwell](https://en.wikipedia.org/wiki/Aloha_Wanderwell), a
Canadian explorer, author, filmmaker, and aviator.

This repository is the consolidation of a bunch of tools that I have written in
the past with similar intents.

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
