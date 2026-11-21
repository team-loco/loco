import argparse
import json
import uuid

from harness import evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Check that resource and deployment status renders as a status badge"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(
    args, "status-badge", "tests/ui/fixtures/settings-labels/scenario.json"
)
org = state["orgs"]["acme"]["id"]
workspace = state["workspaces"]["acme/shared"]
base = state["ui_url"] + f"/org/{org}/wks/{workspace}"
production = evidence.psql(
    state["container"],
    f"SELECT id FROM environments WHERE workspace_id='{workspace}' AND slug='production';",
)
drafts_key = f"loco:drafts:v1:{org}:{workspace}:{production}"


def runtime(replicas):
    return {
        "observability": {"injectOtel": True},
        "build": {"type": "image", "image": "example.invalid/badge:1"},
        "port": 8080,
        "regions": {
            "us-ca-1": {
                "cpu": "100m",
                "memory": "128Mi",
                "minReplicas": replicas,
                "maxReplicas": replicas,
            }
        },
    }


def service():
    return {
        "routing": {"port": 8080},
        "regions": {
            "us-ca-1": {
                "enabled": True,
                "cpu": "100m",
                "memory": "128Mi",
                "minReplicas": 2,
                "maxReplicas": 2,
            }
        },
    }


evidence.seed_resources(
    state,
    [
        {
            "workspace": "acme/shared",
            "name": "badge-healthy",
            "service": service(),
            "deployments": [
                {"environment": "production", "status": "running", "spec": runtime(2)}
            ],
        },
        {
            "workspace": "acme/shared",
            "name": "badge-degraded",
            "service": service(),
            "deployments": [
                {
                    "environment": "production",
                    "status": "running",
                    "spec": runtime(2),
                    "placements": {"us-ca-1": {"ready_replicas": 1}},
                }
            ],
        },
        {
            "workspace": "acme/shared",
            "name": "badge-pending",
            "service": service(),
            "deployments": [],
        },
    ],
)
draft = {
    "id": str(uuid.uuid4()),
    "name": "badge-draft",
    "slug": "badge-draft",
    "image": "registry.example.com/acme/draft:1",
    "createdMs": 0,
    "edits": {},
}

BADGE_STYLE = """el => {
  const s = getComputedStyle(el);
  const alpha = (c) => { const m = c.match(/\\/\\s*([\\d.]+)\\)|rgba\\([^)]*,\\s*([\\d.]+)\\)/); return m ? parseFloat(m[1] ?? m[2]) : 1; };
  return {
    border: parseFloat(s.borderTopWidth),
    borderAlpha: s.borderTopStyle === 'none' ? 0 : alpha(s.borderTopColor),
    fillAlpha: alpha(s.backgroundColor),
    height: el.getBoundingClientRect().height,
    icon: !!el.querySelector('svg'),
  };
}"""


def expect_badge(row, label, tone):
    badge = row.locator("[data-status-badge]").first
    expect(badge).to_have_text(label)
    expect(badge).to_have_attribute("data-status-badge", tone)
    style = badge.evaluate(BADGE_STYLE)
    assert style["icon"], label
    assert style["border"] >= 1 and style["borderAlpha"] > 0, (label, style)
    assert style["fillAlpha"] > 0, (label, style)
    assert 20 <= style["height"] <= 28, (label, style)


def row_of(page, name):
    return (
        page.get_by_role("row")
        .filter(has=page.get_by_role("link", name=name, exact=True))
        .first
    )


def expect_running(page):
    badge = page.locator("[data-status-badge]").filter(has_text="Running").first
    expect(badge).to_be_visible()
    expect(badge).to_have_attribute("data-status-badge", "ok")


def check_dashboard(page):
    page.goto(base + "?env=production")
    expect_badge(row_of(page, "badge-healthy"), "Healthy", "ok")
    expect_badge(row_of(page, "badge-degraded"), "Degraded", "warn")
    expect_badge(row_of(page, "badge-pending"), "Pending", "neutral")
    expect_badge(row_of(page, "badge-draft"), "Draft", "neutral")
    expect_running(page)


def check_resource_page(page):
    page.goto(base + "?env=production")
    row_of(page, "badge-degraded").get_by_role("link", name="badge-degraded").click()
    header = page.get_by_role("heading", name="badge-degraded", exact=True).locator(
        "xpath=ancestor::div[contains(@class,'flex-wrap')][1]"
    )
    expect(header.locator("[data-status-badge]").first).to_have_text("Degraded")
    expect_running(page)


with session(repo / "tests/ui/out/status-badge") as browser:
    for color in ("loco", "solarized", "neobrutal"):
        for mode in ("light", "dark"):
            context, page, errors = open_page(
                browser,
                state=state,
                storage={
                    drafts_key: json.dumps([draft]),
                    "loco:color:v1": color,
                    evidence.MODE_KEY: mode,
                },
            )
            check_dashboard(page)
            if color == "loco" and mode == "light":
                check_resource_page(page)
            assert not errors, errors
            context.close()
print("PASS: resource and deployment status render as status badges in every theme")
