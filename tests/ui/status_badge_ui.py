import argparse
import json
import re
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
    state,
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
            "name": "badge-update-failed",
            "service": service(),
            "deployments": [
                {"environment": "production", "status": "failed", "spec": runtime(2)}
            ],
        },
        {
            "workspace": "acme/shared",
            "name": "badge-updating",
            "service": service(),
            "deployments": [
                {
                    "environment": "production",
                    "status": "deploying",
                    "spec": runtime(2),
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
    radius: parseFloat(s.borderTopLeftRadius),
    height: el.getBoundingClientRect().height,
    icon: !!el.querySelector('svg'),
  };
}"""


color_theme = "loco"


def expect_badge(row, label, tone):
    badge = row.locator("[data-status-badge]").first
    expect(badge).to_have_text(label)
    expect(badge).to_have_attribute("data-status-badge", tone)
    style = badge.evaluate(BADGE_STYLE)
    assert style["icon"], label
    assert style["border"] >= 1 and style["borderAlpha"] > 0, (label, style)
    if color_theme == "paper":
        assert style["fillAlpha"] == 0 and style["radius"] >= 99, (label, style)
    else:
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


FAILED_UPDATE = "Update failed, previous version still serving"


def expect_badges(scope, labels):
    expect(scope.locator("[data-status-badge]")).to_have_text(labels)


def check_dashboard(page):
    page.goto(base + "?env=production")
    expect_badge(row_of(page, "badge-healthy"), "Healthy", "ok")
    expect_badges(row_of(page, "badge-healthy"), ["Healthy"])
    expect_badges(row_of(page, "badge-update-failed"), ["Healthy", "Update failed"])
    expect(
        row_of(page, "badge-update-failed").locator("[data-status-badge]").nth(1)
    ).to_have_attribute("data-status-badge", "err")
    expect_badges(row_of(page, "badge-updating"), ["Healthy", "Updating"])
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
    page.goto(base + "?env=production")
    row_of(page, "badge-update-failed").get_by_role(
        "link", name="badge-update-failed"
    ).click()
    header = page.get_by_role(
        "heading", name="badge-update-failed", exact=True
    ).locator("xpath=ancestor::div[contains(@class,'flex-wrap')][1]")
    expect_badges(header, ["Healthy", FAILED_UPDATE])


def check_resources_table(page):
    page.goto(base + "/resources?env=production")
    expect(row_of(page, "badge-update-failed")).to_contain_text("update failed")
    expect(row_of(page, "badge-updating")).to_contain_text("updating")
    expect(row_of(page, "badge-healthy")).not_to_contain_text("update")


def check_activity(page):
    page.goto(base + "?env=production")
    page.get_by_role("button", name=re.compile(r"^Workspace activity, ")).click()
    drawer = page.get_by_role("dialog", name="Workspace activity", exact=True)
    card = (
        drawer.get_by_role("listitem")
        .filter(has=page.get_by_role("link", name="badge-update-failed", exact=True))
        .filter(has_text=FAILED_UPDATE)
    )
    expect(card.get_by_text("Healthy", exact=True)).to_be_visible()
    expect(card.get_by_text(FAILED_UPDATE, exact=True)).to_be_visible()


with session(repo / "tests/ui/out/status-badge") as browser:
    for color in ("loco", "solarized", "neobrutal", "paper"):
        color_theme = color
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
                check_resources_table(page)
                check_activity(page)
            assert not errors, errors
            context.close()
print("PASS: resource and deployment status render as status badges in every theme")
