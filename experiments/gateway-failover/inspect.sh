#!/bin/bash
# Show the generated Envoy cluster: priority levels and live host health.
R=${1:-eu}; CTX="kind-fo-$R"
POD=$(kubectl --context "$CTX" get pods -n envoy-gateway-system -l gateway.envoyproxy.io/owning-gateway-name=eg -o jsonpath='{.items[0].metadata.name}')
kubectl --context "$CTX" port-forward -n envoy-gateway-system "$POD" 19000:19000 >/dev/null 2>&1 &
PF=$!; trap 'kill $PF 2>/dev/null' EXIT; sleep 3
echo "--- priority levels (from config_dump) ---"
curl -s localhost:19000/config_dump | python3 -c "
import json,sys
d=json.load(sys.stdin)
for c in d['configs']:
    for cl in c.get('dynamic_active_clusters',[]):
        n=cl['cluster']['name']
        if 'demo-app/rule/1' not in n: continue
        for ep in cl['cluster'].get('load_assignment',{}).get('endpoints',[]):
            hosts=[list(x['endpoint']['address'].values())[0] for x in ep.get('lb_endpoints',[])]
            print('  priority=%s %s' % (ep.get('priority',0), hosts))
"
echo "--- live host health (from /clusters) ---"
curl -s "localhost:19000/clusters?format=json" | python3 -c "
import json,sys
d=json.load(sys.stdin)
for c in d.get('cluster_statuses',[]):
    if 'demo-app/rule/1' not in c.get('name',''): continue
    for h in c.get('host_statuses',[]):
        a=h['address'].get('socket_address',{})
        print('  %s:%s -> %s' % (a.get('address'), a.get('port_value'), h.get('health_status')))
"
