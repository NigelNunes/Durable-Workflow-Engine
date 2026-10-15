#!/bin/sh
# Start two of each scenario, crash one worker during the onboarding timer,
# and confirm every workflow reaches its expected result.
set -eu

ENGINE_URL=${ENGINE_URL:-http://localhost:8080}
SERVICES_URL=${SERVICES_URL:-http://localhost:8081}
DEMO_KEY=${DEMO_KEY:-dev-demo-key}
ADMIN_TOKEN=${ADMIN_TOKEN:-dev-admin-token}
PREFIX=${PREFIX:-mix}

start_one() {
	id="$1"
	type="$2"
	input="$3"
	curl -fsS -X POST "$ENGINE_URL/v1/workflows" \
		-H "X-Demo-Key: $DEMO_KEY" \
		-H "Content-Type: application/json" \
		-d "{\"id\":\"${id}\",\"type\":\"${type}\",\"input\":${input}}"
	echo
}

i=1
while [ "$i" -le 2 ]; do
	start_one "${PREFIX}-ord-${i}" order_fulfilment "{\"order_id\":\"${PREFIX}-ord-${i}\",\"amount_cents\":2500,\"sku\":\"widget\",\"qty\":1,\"email\":\"buyer@example.com\"}"
	start_one "${PREFIX}-onb-${i}" user_onboarding "{\"user_id\":\"${PREFIX}-onb-${i}\",\"email\":\"new@example.com\",\"delay_seconds\":30}"
	start_one "${PREFIX}-xfer-${i}" money_transfer "{\"transfer_id\":\"${PREFIX}-xfer-${i}\",\"from_account\":\"alice\",\"to_account\":\"bob\",\"amount_cents\":1000}"
	start_one "${PREFIX}-rpt-${i}" report_pipeline "{\"report_id\":\"${PREFIX}-rpt-${i}\"}"
	i=$((i + 1))
done

before=""
for c in durable-flow-demo-worker-1-1 durable-flow-demo-worker-2-1 durable-flow-demo-worker-3-1; do
	before="$before $(docker inspect -f '{{.RestartCount}}' "$c")"
done

sleep 2
curl -fsS -X POST "$ENGINE_URL/v1/admin/chaos" \
	-H "X-Demo-Key: $DEMO_KEY" \
	-H "X-Admin-Token: $ADMIN_TOKEN" \
	-H "Content-Type: application/json" \
	-d '{"action":"crash"}'
echo

ENGINE_URL="$ENGINE_URL" DEMO_KEY="$DEMO_KEY" PREFIX="$PREFIX" python3 - <<'PY'
import json, os, time, urllib.request

engine = os.environ["ENGINE_URL"]
key = os.environ["DEMO_KEY"]
prefix = os.environ["PREFIX"]
ids = []
for kind in ("ord", "onb", "xfer", "rpt"):
    for i in (1, 2):
        ids.append(f"{prefix}-{kind}-{i}")
want = {
    "ord": "fulfilled",
    "onb": "onboarded",
    "xfer": "transferred",
    "rpt": "completed",
}
deadline = time.time() + 120

def workflow(workflow_id):
    req = urllib.request.Request(
        f"{engine}/v1/workflows/{workflow_id}",
        headers={"X-Demo-Key": key},
    )
    with urllib.request.urlopen(req, timeout=10) as res:
        return json.load(res)

while time.time() < deadline:
    states = [workflow(workflow_id) for workflow_id in ids]
    if any(wf["status"] == "failed" for wf in states):
        raise SystemExit("a workflow failed: " + json.dumps(states))
    if all(wf["status"] == "completed" for wf in states):
        for wf in states:
            kind = wf["id"].split("-")[1]
            if wf["result"]["status"] != want[kind]:
                raise SystemExit("unexpected result: " + json.dumps(wf))
        print("8 workflows completed")
        break
    time.sleep(0.5)
else:
    raise SystemExit("timed out waiting for the mix")
PY

sleep 6
after=""
for c in durable-flow-demo-worker-1-1 durable-flow-demo-worker-2-1 durable-flow-demo-worker-3-1; do
	after="$after $(docker inspect -f '{{.RestartCount}}' "$c")"
done
echo "restart before:$before"
echo "restart after:$after"
if [ "$before" = "$after" ]; then
	echo "no worker restarted" >&2
	exit 1
fi

stats=$(curl -fsS "$SERVICES_URL/stats")
echo "$stats"
dups=$(printf '%s' "$stats" | python3 -c 'import json,sys; print(json.load(sys.stdin)["duplicate_charges"])')
if [ "$dups" != "0" ]; then
	echo "duplicate_charges is $dups" >&2
	exit 1
fi

curl -fsS -X POST "$SERVICES_URL/admin/credit/fail" \
	-H "Content-Type: application/json" \
	-d '{"enabled":true}'
echo
fail_id="${PREFIX}-xfer-fail"
start_one "$fail_id" money_transfer "{\"transfer_id\":\"${fail_id}\",\"from_account\":\"alice\",\"to_account\":\"bob\",\"amount_cents\":1000}"

ENGINE_URL="$ENGINE_URL" DEMO_KEY="$DEMO_KEY" FAIL_ID="$fail_id" python3 - <<'PY'
import json, os, time, urllib.request

engine = os.environ["ENGINE_URL"]
key = os.environ["DEMO_KEY"]
workflow_id = os.environ["FAIL_ID"]
deadline = time.time() + 60

def workflow():
    req = urllib.request.Request(
        f"{engine}/v1/workflows/{workflow_id}",
        headers={"X-Demo-Key": key},
    )
    with urllib.request.urlopen(req, timeout=10) as res:
        return json.load(res)

while time.time() < deadline:
    wf = workflow()
    if wf["status"] == "failed":
        raise SystemExit("rollback workflow failed: " + json.dumps(wf))
    if wf["status"] == "completed":
        if wf["result"]["status"] != "rolled_back":
            raise SystemExit("unexpected rollback result: " + json.dumps(wf["result"]))
        print("transfer rolled back")
        break
    time.sleep(0.3)
else:
    raise SystemExit("timed out waiting for rollback")
PY

kinds=$(docker exec durable-flow-postgres-1 psql -U durable -d durable -tA -c \
	"SELECT kind FROM demo.transfers_log WHERE idempotency_key LIKE '${fail_id}:%' ORDER BY kind")
echo "rollback kinds: $kinds"
printf '%s\n' "$kinds" | grep -qx debit
printf '%s\n' "$kinds" | grep -qx refund
if printf '%s\n' "$kinds" | grep -qx credit; then
	echo "credit was written during rollback" >&2
	exit 1
fi

curl -fsS -X POST "$SERVICES_URL/admin/credit/fail" \
	-H "Content-Type: application/json" \
	-d '{"enabled":false}'
echo
