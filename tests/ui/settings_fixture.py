import uuid

from harness import evidence

REGION = "us-ca-1"


def seed_settings_resource(state, prefix, cpu="100m", memory="128Mi", health=True):
    """Seeds a running service, with a health check unless health is False; returns its name, id and settings URL."""
    workspace = state["workspaces"]["acme/shared"]
    org = state["orgs"]["acme"]["id"]
    name = f"{prefix}-{uuid.uuid4().hex[:8]}"
    spec = {
        "platform": "linux/amd64",
        "build": {"type": "image", "image": "registry.example.com/app:1"},
        "regions": {
            REGION: {
                "cpu": cpu,
                "memory": memory,
                "minReplicas": 1,
                "maxReplicas": 1,
            }
        },
        "port": 8080,
        "observability": {"injectOtel": True},
        "healthCheck": {
            "path": "/health",
            "initialDelaySeconds": 5,
            "intervalSeconds": 30,
            "timeoutSeconds": 5,
            "failureThreshold": 3,
        },
    }
    if not health:
        del spec["healthCheck"]
    resource = evidence.seed_resources(
        state,
        [
            {
                "workspace": "acme/shared",
                "name": name,
                "deployments": [
                    {
                        "environment": "production",
                        "placements": {
                            REGION: {"cluster": evidence.seed_cluster(state, REGION)}
                        },
                        "spec": spec,
                    }
                ],
            }
        ],
    )[f"acme/shared/{name}"]
    base = f"{state['ui_url']}/org/{org}/wks/{workspace}/resource/{resource}"
    return name, resource, base
