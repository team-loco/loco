import uuid

from harness import evidence

REGION = "us-east-1"


def seed_settings_resource(state, prefix, *, health=True):
    """Seeds a running service with a health check; returns its name, id and settings URL."""
    workspace = state["workspaces"]["acme/shared"]
    org = state["orgs"]["acme"]["id"]
    name = f"{prefix}-{uuid.uuid4().hex[:8]}"
    spec = {
        "platform": "linux/amd64",
        "build": {"type": "image", "image": "registry.example.com/app:1"},
        "cpu": "100m",
        "memory": "128Mi",
        "minReplicas": 1,
        "maxReplicas": 1,
        "port": 8080,
        "observability": {"injectOtel": True},
    }
    if health:
        spec["healthCheck"] = {
            "path": "/health",
            "initialDelaySeconds": 5,
            "intervalSeconds": 30,
            "timeoutSeconds": 5,
            "failureThreshold": 3,
        }
    resource = evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": name,
                "deployments": [
                    {
                        "cluster": evidence.seed_cluster(state, REGION),
                        "environment": "production",
                        "region": REGION,
                        "spec": spec,
                    }
                ],
            }
        ],
    )[f"acme/shared/{name}"]
    base = f"{state['ui_url']}/org/{org}/wks/{workspace}/resource/{resource}"
    return name, resource, base
