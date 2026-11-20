from harness import call, evidence, sql

ACTIVITY_SERVICE = {
    "routing": {"port": 8080},
    "regions": {"us-east-1": {"enabled": True, "cpu": "100m", "memory": "128Mi"}},
}


def prepare_activity(state):
    assert state["container"].startswith(evidence.LABEL + "-")
    if "activity" in state:
        return state["activity"]
    org = state["orgs"]["acme"]["id"]
    workspace = state["workspaces"]["acme/shared"]
    sibling = state["workspaces"]["acme/sibling"]
    call(
        state,
        "loco.environment.v1.EnvironmentService/CreateEnvironment",
        {"workspaceId": workspace, "name": "staging"},
    )
    resources = evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": "checkout",
                "service": ACTIVITY_SERVICE,
            },
            {
                "workspace": "acme/sibling",
                "name": "inventory",
                "service": ACTIVITY_SERVICE,
            },
        ],
    )
    resource = resources["acme/shared/checkout"]
    sibling_resource = resources["acme/sibling/inventory"]
    other = call(state, evidence.RPC_CREATE_ORG, {"name": "other-company"})["orgId"]
    other_workspace = call(
        state,
        "loco.workspace.v1.WorkspaceService/CreateWorkspace",
        {"orgId": other, "name": "internal"},
    )["workspaceId"]
    scopes = [
        (state["users"][name]["id"], "member", "organization", other)
        for name in ("workspace_admin", "resource_reader")
    ]
    scopes.extend(
        (state["users"]["workspace_admin"]["id"], scope, "workspace", workspace)
        for scope in ("read", "write", "admin")
    )
    scopes.extend(
        (state["users"][name]["id"], "read", "resource", resource)
        for name in ("resource_reader", "scoped")
    )
    values = ",".join(
        "(" + ",".join(evidence.sql_literal(value) for value in row) + ")"
        for row in scopes
    )
    sql(
        state,
        "INSERT INTO user_scopes(user_id,scope,entity_type,entity_id) VALUES "
        + values
        + ";",
    )
    for index in range(53):
        call(
            state,
            "loco.resource.v1.ResourceService/UpdateResource",
            {
                "resourceId": resource,
                "description": f"Checkout activity revision {index}",
            },
        )
    environments = call(
        state,
        "loco.environment.v1.EnvironmentService/ListEnvironments",
        {"workspaceId": workspace},
    )["environments"]
    production = next(e["id"] for e in environments if e["slug"] == "production")
    staging = next(e["id"] for e in environments if e["slug"] == "staging")
    sql(
        state,
        f"""INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,actor_type,actor_id)
        SELECT 'resource.restarted','{org}','{workspace}','{resource}','{production}','user','{state["users"]["owner"]["id"]}' FROM generate_series(1,23);
        INSERT INTO events(type,org_id,workspace_id,resource_id,environment_id,actor_type,actor_id,data)
        VALUES('secret.set','{org}','{workspace}','{resource}','{staging}','user','{state["users"]["owner"]["id"]}','{{"names":["STAGING_ONLY"]}}');""",
    )
    call(
        state,
        "loco.workspace.v1.WorkspaceService/UpdateWorkspace",
        {
            "workspaceId": workspace,
            "description": "Commerce services managed by the checkout team",
        },
        user="workspace_admin",
    )
    return {
        "orgId": org,
        "workspaceId": workspace,
        "siblingId": sibling,
        "resourceId": resource,
        "siblingResourceId": sibling_resource,
        "otherOrgId": other,
        "otherWorkspaceId": other_workspace,
    }


def verify_activity_access(state, activity):
    method = "loco.event.v1.EventService/ListEvents"
    workspace_scope = {
        "orgId": activity["orgId"],
        "workspaceId": activity["workspaceId"],
    }
    resource_scope = {**workspace_scope, "resourceId": activity["resourceId"]}
    for user, scope in [
        ("workspace_admin", workspace_scope),
        ("resource_reader", resource_scope),
    ]:
        granted = call(state, "loco.token.v1.TokenService/GetScopes", {}, user=user)[
            "scopes"
        ]
        assert not any(
            item.get("entityType") == "ENTITY_TYPE_ORGANIZATION"
            and item.get("entityId") == activity["orgId"]
            for item in granted
        )
        first = call(state, method, {**scope, "pageSize": 50}, user=user)
        assert len(first["events"]) == 50 and first.get("nextBeforeSeq")
        assert all(
            event["orgId"] == activity["orgId"]
            and event["workspaceId"] == activity["workspaceId"]
            for event in first["events"]
        )
        if user == "resource_reader":
            assert all(
                event["resourceId"] == activity["resourceId"]
                for event in first["events"]
            )
        second = call(
            state,
            method,
            {**scope, "pageSize": 50, "beforeSeq": first["nextBeforeSeq"]},
            user=user,
        )
        assert second["events"]
        assert not {event["id"] for event in first["events"]} & {
            event["id"] for event in second["events"]
        }
        filtered = call(
            state, method, {**scope, "query": "@type:resource.created"}, user=user
        )
        assert (
            len(filtered["events"]) == 1
            and filtered["events"][0]["type"] == "resource.created"
        )
        denied = [
            {"orgId": activity["orgId"]},
            {"orgId": activity["orgId"], "workspaceId": activity["siblingId"]},
            {
                "orgId": activity["otherOrgId"],
                "workspaceId": activity["otherWorkspaceId"],
            },
        ]
        if user == "resource_reader":
            denied.extend(
                [
                    workspace_scope,
                    {**workspace_scope, "resourceId": activity["siblingResourceId"]},
                ]
            )
        for body in denied:
            try:
                call(state, method, body, user=user)
            except evidence.RPCError as error:
                assert error.code == "permission_denied", (user, body, error.code)
            else:
                raise AssertionError((user, body, "unexpected access"))
        try:
            call(state, method, {**scope, "orgId": activity["otherOrgId"]}, user=user)
        except evidence.RPCError as error:
            assert error.code == "not_found", (user, error.code)
        else:
            raise AssertionError((user, "mismatched parent accepted"))
