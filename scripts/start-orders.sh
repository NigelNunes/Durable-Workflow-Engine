#!/bin/sh
# Start COUNT order workflows and wait until each one is fulfilled.
# duplicate_charges must stay 0.
set -eu

ENGINE_URL=${ENGINE_URL:-http://localhost:8080}
SERVICES_URL=${SERVICES_URL:-http://localhost:8081}
DEMO_KEY=${DEMO_KEY:-dev-demo-key}
PREFIX=${PREFIX:-ord}
COUNT=${COUNT:-12}

i=1
while [ "$i" -le "$COUNT" ]; do
	id="${PREFIX}-${i}"
	curl -fsS -X POST "$ENGINE_URL/v1/workflows" \
		-H "X-Demo-Key: $DEMO_KEY" \
		-H "Content-Type: application/json" \
		-d "{\"id\":\"${id}\",\"type\":\"order_fulfilment\",\"input\":{\"order_id\":\"${id}\",\"amount_cents\":2500,\"sku\":\"widget\",\"qty\":1,\"email\":\"buyer@example.com\"}}"
	echo
	i=$((i + 1))
done

ENGINE_URL="$ENGINE_URL" DEMO_KEY="$DEMO_KEY" PREFIX="$PREFIX" COUNT="$COUNT" python3 - <<'PY'
import json, os, time, urllib.request

engine = os.environ["ENGINE_URL"]
key = os.environ["DEMO_KEY"]
prefix = os.environ["PREFIX"]
count = int(os.environ["COUNT"])
deadline = time.time() + 90

def workflow(i):
    req = urllib.request.Request(
        f"{engine}/v1/workflows/{prefix}-{i}",
        headers={"X-Demo-Key": key},
    )
    with urllib.request.urlopen(req, timeout=10) as res:
        return json.load(res)

while time.time() < deadline:
    states = [workflow(i) for i in range(1, count + 1)]
    if any(wf["status"] == "failed" for wf in states):
        raise SystemExit("a workflow failed: " + json.dumps(states))
    if all(wf["status"] == "completed" for wf in states):
        for wf in states:
            if wf["result"]["status"] != "fulfilled":
                raise SystemExit("unexpected result: " + json.dumps(wf["result"]))
        print(f"{count} orders fulfilled")
        break
    time.sleep(0.3)
else:
    raise SystemExit("timed out waiting for orders")
PY

stats=$(curl -fsS "$SERVICES_URL/stats")
echo "$stats"
dups=$(printf '%s' "$stats" | python3 -c 'import json,sys; print(json.load(sys.stdin)["duplicate_charges"])')
if [ "$dups" != "0" ]; then
	echo "duplicate_charges is $dups" >&2
	exit 1
fi
