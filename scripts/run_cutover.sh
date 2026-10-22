#!/bin/sh
set -eu

curl --fail-with-body \
  --request POST \
  --header 'Content-Type: application/json' \
  --data '{
    "cutover_id": "cut-1042",
    "ttl": 60,
    "seller_asset": {
      "domain": "example.com",
      "record_name": "market.example.com",
      "previous_target": "old.market.example",
      "new_target": "new.market.example"
    },
    "buyer_update": {
      "buyer_id": "buyer-88",
      "message": "Marketplace handoff is ready."
    },
    "order_handoff": {
      "order_id": "order-501",
      "state": "pending_dns"
    }
  }' \
  http://localhost:8080/cutovers
