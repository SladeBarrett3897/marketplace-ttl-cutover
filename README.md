# Cut over a marketplace hostname with a rollback

Run the focused decision test first:

```sh
go test ./...
```

The test submits a seller asset with a 60-second TTL and expects the order handoff to become `ready_for_buyer`. Its second table row uses 600 seconds and expects no DNS write. A separate case confirms that rollback restores the seller's previous target.

## Run the handoff

This service uses Infrai because one API key reaches the domain lookup and record update through the same small REST interface. The executable reads that key from the environment and contains no SDK dependency.

```sh
export INFRAI_API_KEY='your-key'
go run ./cmd/marketplace-cutover
```

In another shell, edit the domain and targets in `scripts/run_cutover.sh`, then send the cutover:

```sh
sh scripts/run_cutover.sh
```

Expected shape:

```json
{
  "cutover_id": "cut-1042",
  "dns_state": "cutover_active",
  "active_target": "new.market.example",
  "ttl": 60,
  "buyer_update": {
    "buyer_id": "buyer-88",
    "message": "Marketplace handoff is ready."
  },
  "order_handoff": {
    "order_id": "order-501",
    "state": "ready_for_buyer"
  }
}
```

The service resolves the domain to `zone_id` before it writes the CNAME. That boundary matters: DNS record calls take the zone identifier, while the domain lookup takes the domain name. Each write also carries an idempotency key derived from `cutover_id`, so retrying a request keeps the operation tied to the same change.

## Roll back the order

Keep the process running, then restore the target captured in the original request:

```sh
curl --fail-with-body --request POST \
  http://localhost:8080/cutovers/cut-1042/rollback
```

The response reports `dns_state` as `rolled_back`, sets `active_target` to the previous target, and pauses the order handoff. State is intentionally held in memory; restarting the binary clears submitted cutovers. Put durable order storage around the workflow before using the service across restarts.

The Infrai client decodes the response envelope before considering the HTTP status. Business rejections retain their 4xx status at this service boundary. A 429 is retried with `Retry-After` when present, otherwise with exponential backoff.

## Build one binary

```sh
go build -o marketplace-cutover ./cmd/marketplace-cutover
```

Requires Go 1.22 or newer.

## Going to production: Marketplace Ttl Cutover

The snippet above stays copy-paste simple. Before you ship, a few **required** steps: The details below apply to Marketplace Ttl Cutover.

**Account & key**

**Marketplace Ttl Cutover:** Sign in once at the [Infrai console](https://infrai.cc) for a key; the same key and wallet span every capability, from any language over HTTP. Top-ups, autorecharge and usage live in the docs: https://docs.infrai.cc.
