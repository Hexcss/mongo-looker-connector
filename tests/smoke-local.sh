#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export COMPOSE_PROJECT_NAME="mlc-smoke-$$"
export GATEWAY_PORT=0
trap 'docker compose down --volumes --remove-orphans >/dev/null' EXIT

docker compose up --build --detach --wait --wait-timeout 120
gateway="http://$(docker compose port gateway 8080)"
for attempt in {1..30}; do
  if curl --fail --silent "$gateway/health" >/dev/null; then break; fi
  sleep 1
done

get() { curl --fail-with-body --silent --show-error "$gateway$1"; }
post() {
  curl --fail-with-body --silent --show-error --json "$2" "$gateway$1"
}

get /health >/dev/null
get /version | jq -e --arg expected "$(cat VERSION)" '.version == $expected'
get /api/v1/connections | jq -e '.connections == ["demo"]'
get /api/v1/connections/demo/databases | jq -e '.databases == ["analytics"]'
get '/api/v1/collections?connectionId=demo&database=analytics' | jq -e '.collections == ["orders","shipping_zones"]'
scope='{"connectionId":"demo","database":"analytics","collection":"orders"}'
post /api/v1/schema "$scope" | jq -e 'any(.fields[]; .name == "amount" and .type == "NUMBER")'
query=$(jq -c '. + {fields:["amount","_id"],pipeline:[{"$match":{"_id":"order-001"}}]}' <<<"$scope")
post /api/v1/query "$query" | jq -e '.fields == ["amount","_id"] and .rows == [[42,"order-001"]]'
joined=$(jq -c '. + {fields:["amount","region"],pipeline:[{"$match":{"amount":42}},{"$lookup":{"from":"shipping_zones","localField":"_id","foreignField":"_id","as":"zone"}},{"$unwind":"$zone"},{"$project":{"amount":1,"region":"$zone.region"}}]}' <<<"$scope")
post /api/v1/query "$joined" | jq -e '.rows == [[42,"EU-South"]]'
denied=$(jq -c '.collection = "unpublished"' <<<"$query")
status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' --json "$denied" "$gateway/api/v1/query")
[[ "$status" == 404 ]]
echo 'MongoDB gateway smoke passed.'
