"""Synthetic typed allocation replies for UI behavior; not backend scale evidence."""

import time

CLUSTER = "**/loco.cluster.v1.ClusterAdminService/"
TOP = [1200, 800, 600, 500, 400, 300, 250, 200, 150, 100, 75, 50]
MIB = 1024**2


def install_capacity(context, state):
    calls = []
    org_id = next(iter(state["orgs"].values()))["id"]
    workspace_id = next(iter(state["workspaces"].values()))
    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

    def consumer(index, workspace=""):
        value = TOP[index] if index < len(TOP) else 1
        item = {
            "id": f"b0000000-0000-4000-8000-{index:012d}",
            "name": f"Workspace {index:04d}",
            "kind": "workspace",
            "orgId": org_id,
            "orgName": "Synthetic organization",
            "workspaceId": workspace_id,
            "cpuMillicores": str(value),
            "memoryBytes": str(value * 2 * MIB),
            "resourceCount": 100,
        }
        if index == 0:
            item["id"] = workspace_id
        if workspace:
            item.update(
                id=f"c0000000-0000-4000-8000-{index:012d}",
                name=f"Resource {index:04d}",
                kind="resource",
                workspaceId=workspace,
                cpuMillicores="12",
                memoryBytes=str(24 * MIB),
                resourceCount=1,
            )
        return item

    def nodes(route):
        body = route.request.post_data_json
        calls.append(("nodes", body))
        start = body.get("page", 0) * 10
        indices = range(start, min(1000, start + 10))
        query = body.get("query", "")
        if query:
            indices = [i for i in range(1000) if query in f"node-{i:04d}"]
        rows = [node(f"node-{i:04d}") for i in list(indices)[:10]]
        total = len(indices) if query else 1000
        route.fulfill(
            json={
                "sample": {"id": "synthetic:" + body["clusterId"], "reportedAt": now},
                "nodes": rows,
                "totalCount": total,
                "resourceCount": 100000,
            }
        )

    def node(name):
        requested = sum(TOP) + 988 + 500
        return {
            "name": name,
            "ready": True,
            "cpuMillicoresAllocatable": "8000",
            "memoryBytesAllocatable": str(16 * 1024**3),
            "cpuMillicoresRequested": str(requested),
            "memoryBytesRequested": str(requested * 2 * MIB),
            "capacityReportedAt": now,
            "resourceCount": 100000,
        }

    def inspector(route):
        body = route.request.post_data_json
        calls.append(("inspector", body))
        assert body["sampleId"] == "synthetic:" + body["clusterId"]
        workspace = body.get("workspaceId", "")
        count = 100 if workspace else 1000
        indices = list(range(count))
        if body.get("remainderOnly"):
            indices = indices[12:]
        query = body.get("query", "")
        if query:
            indices = [
                i
                for i in indices
                if query.lower() in consumer(i, workspace)["name"].lower()
            ]
        page = body.get("page", 0)
        remainder = 1056 if workspace else 988
        result = {
            "sample": {"id": body["sampleId"], "reportedAt": now},
            "node": node(body["node"]),
            "top": [consumer(i, workspace) for i in range(12)],
            "remainder": {
                "id": "other",
                "name": "Other",
                "kind": "other",
                "cpuMillicores": str(remainder),
                "memoryBytes": str(remainder * 2 * MIB),
            },
            "consumers": [
                consumer(i, workspace) for i in indices[page * 10 : (page + 1) * 10]
            ],
            "totalCount": len(indices),
            "memory": body.get("memory", False),
            "workspaceId": workspace,
            "workspaceName": "Workspace 0000" if workspace else "",
        }
        if not workspace:
            result["system"] = {
                "id": "system",
                "name": "System",
                "kind": "system",
                "cpuMillicores": "500",
                "memoryBytes": str(1000 * MIB),
            }
        route.fulfill(json=result)

    context.route(CLUSTER + "ListClusterNodes", nodes)
    context.route(CLUSTER + "GetNodeAllocations", inspector)
    return calls
