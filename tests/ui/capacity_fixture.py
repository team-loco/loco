"""Synthetic typed allocation replies for UI behavior; not backend scale evidence."""

import time

from harness import call

CLUSTER = "**/loco.cluster.v1.ClusterAdminService/"
TOP = [1200, 800, 600, 500, 400, 300, 250, 200, 150, 100, 75, 50]
MIB = 1024**2


def install_capacity(context, state):
    calls = []
    org_id = next(iter(state["orgs"].values()))["id"]
    workspace_id = next(iter(state["workspaces"].values()))
    now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    clusters = call(
        state, "loco.cluster.v1.ClusterAdminService/ListClusters", {}, user="admin"
    )["clusters"]
    names = {cluster["id"]: cluster["name"] for cluster in clusters}

    def consumer(index, workspace=""):
        value = TOP[index] if index < len(TOP) else 1
        item = {
            "id": f"b0000000-0000-4000-8000-{index:012d}",
            "name": f"Workspace {index:04d}",
            "kind": "workspace",
            "orgId": org_id,
            "orgName": "Synthetic organization",
            "orgLinkAvailable": True,
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
                resourceLinkAvailable=False,
                orgLinkAvailable=True,
            )
        if workspace and index == 0 and state.get("resources"):
            item["id"] = next(iter(state["resources"].values()))
            item["name"] = "capacity-api"
            item["resourceLinkAvailable"] = True
        return item

    def summary(cluster_id):
        requested = (sum(TOP) + 988 + 500) * 1000
        result = {
            "clusterId": cluster_id,
            "sample": {"id": "synthetic:" + cluster_id, "reportedAt": now},
            "cpuMillicoresRequested": str(requested),
            "memoryBytesRequested": str(requested * 2 * MIB),
            "cpuMillicoresAllocatable": "8000000",
            "memoryBytesAllocatable": str(16000 * 1024**3),
            "capacityReportedAt": now,
            "resourceCount": 100000,
            "nodeCount": 1000,
        }
        if names.get(cluster_id) == "de-he-1":
            result["sample"]["stale"] = True
        if names.get(cluster_id) == "de-he-1-b":
            result.pop("sample")
            result.pop("cpuMillicoresAllocatable")
            result.pop("memoryBytesAllocatable")
        return result

    def fleet(route):
        route.fulfill(
            json={"capacities": [summary(cluster["id"]) for cluster in clusters]}
        )

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
        result = summary(body["clusterId"])
        result.pop("clusterId")
        result.pop("nodeCount")
        result.update(nodes=rows, totalCount=total, resourceCount=100000)
        route.fulfill(json=result)

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
            "sample": {
                "id": body["sampleId"],
                "reportedAt": now,
                "stale": names.get(body["clusterId"]) == "de-he-1",
            },
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
            "remainderOnly": body.get("remainderOnly", False),
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

    context.route(CLUSTER + "ListClusterCapacities", fleet)
    context.route(CLUSTER + "ListClusterNodes", nodes)
    context.route(CLUSTER + "GetNodeAllocations", inspector)
    return calls


def install_inventory(context, state):
    """Bounded synthetic inventory reads; navigation still uses real seeded entities."""
    calls = []
    org = state["orgs"]["acme"]["id"]
    workspace = state["workspaces"]["acme/shared"]
    resource = state["resources"]["acme/shared/capacity-api"]
    coverage = {"reportingClusters": 1, "missingClusters": 1}

    def workspaces(route):
        body = route.request.post_data_json
        calls.append(("workspaces", body))
        rows = [
            {
                "workspaceId": workspace
                if i == 0
                else f"d0000000-0000-4000-8000-{i:012d}",
                "orgId": org,
                "name": "shared" if i == 0 else f"Workspace {i:04d}",
                "resourceCount": 1,
                "cpuMillicores": str(1000 - i),
                "memoryBytes": str((i + 1) * MIB),
            }
            for i in range(31)
        ]
        query = body.get("query", "").lower()
        rows = [row for row in rows if query in row["name"].lower()]
        key = "memoryBytes" if body.get("memory") else "cpuMillicores"
        rows.sort(key=lambda row: int(row[key]), reverse=not body.get("ascending"))
        start = body.get("page", 0) * 25
        route.fulfill(
            json={
                "workspaces": rows[start : start + 25],
                "totalCount": len(rows),
                "coverage": coverage,
                "memory": body.get("memory", False),
            }
        )

    def resources(route):
        body = route.request.post_data_json
        calls.append(("resources", body))
        assert body["workspaceId"] == workspace, body
        assert body.get("environmentId"), body
        assert body.get("resourceIds") == [resource], body
        assert body.get("pageSize") == 1, body
        route.fulfill(
            json={
                "workspaceId": workspace,
                "environmentId": body["environmentId"],
                "resources": [
                    {
                        "resourceId": resource,
                        "name": "capacity-api",
                        "cpuMillicores": "125",
                        "memoryBytes": str(64 * MIB),
                        "workspaceBuildCpuMillicores": "300",
                        "workspaceBuildMemoryBytes": str(128 * MIB),
                    }
                ],
                "totalCount": 1,
                "coverage": coverage,
            }
        )

    context.route(CLUSTER + "ListWorkspaceAllocations", workspaces)
    context.route(
        "**/loco.resource.v1.ResourceService/ListResourceAllocations", resources
    )
    return calls
