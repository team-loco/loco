import argparse
import json
import re
import uuid
from pathlib import Path

from harness import (
    call,
    copied_text,
    evidence,
    expect_rpc,
    load_state,
    open_page,
    repo,
    session,
    sql,
)
from playwright.sync_api import expect

parser = argparse.ArgumentParser(
    description="Create, update, import, reference and delete the secrets of an environment"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/secrets"))
args = parser.parse_args()
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
state = load_state(
    args, "secrets", "tests/ui/fixtures/deployment-history/scenario.json"
)
workspace = state["workspaces"]["acme/shared"]
org = state["orgs"]["acme"]["id"]
production = next(
    env["id"]
    for env in call(
        state,
        "loco.environment.v1.EnvironmentService/ListEnvironments",
        {"workspaceId": workspace},
    )["environments"]
    if env["name"] == "production"
)
name = "api-" + uuid.uuid4().hex[:6]
resource = evidence.seed_resources(
    state,
    [
        {
            "workspace": "acme/shared",
            "name": name,
            "deployments": [
                {
                    "environment": "production",
                    "status": "running",
                    "placements": {"us-ca-1": {"ready_replicas": 1}},
                    "spec": {
                        "build": {
                            "type": "image",
                            "image": "registry.example.com/app:1",
                        },
                        "port": 8080,
                        "regions": {
                            "us-ca-1": {
                                "cpu": "100m",
                                "memory": "128Mi",
                                "minReplicas": 1,
                                "maxReplicas": 1,
                            }
                        },
                    },
                }
            ],
        }
    ],
)[f"acme/shared/{name}"]
page_url = state["ui_url"] + f"/org/{org}/wks/{workspace}/secrets?env=production"
secrets_service = "loco.secret.v1.SecretService/"


def stored(secret):
    """The ciphertext row of a secret, which never holds the value."""
    return sql(
        state,
        f"SELECT encode(ciphertext, 'escape') FROM secrets WHERE environment_id='{production}' AND name='{secret}';",
    )


with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    page.goto(state["ui_url"] + f"/org/{org}/wks/{workspace}?env=production")
    page.get_by_role("link", name="Secrets", exact=True).click()
    expect(page).to_have_url(re.compile(r"/secrets\?env=production$"))
    expect(page.get_by_role("heading", name="No secrets in production")).to_be_visible()
    expect(page.get_by_role("columnheader", name="Used by", exact=True)).to_be_visible()
    expect(
        page.get_by_role("textbox", name="Search secrets", exact=True)
    ).to_be_visible()
    expect(
        page.get_by_text(
            "Secrets belong to an environment, and this one is production. Any resource here can refer to one from its variables.",
            exact=True,
        )
    ).to_be_visible()

    page.get_by_role("button", name="New secret", exact=True).first.click()
    dialog = page.get_by_role("dialog", name="New secret in production")
    name_input = dialog.get_by_role("textbox", name="Name", exact=True)
    value = dialog.get_by_label("Value", exact=True)
    name_input.fill("database-url")
    expect(name_input).to_have_value("DATABASE_URL")
    expect(
        dialog.get_by_text(
            "Refer to it from a resource’s variables as ${{ secrets.DATABASE_URL }}.",
            exact=True,
        )
    ).to_be_visible()
    expect(
        dialog.get_by_text(
            "You won’t be able to see this value again after you save.",
            exact=True,
        )
    ).to_be_visible()
    create = dialog.get_by_role("button", name="Create secret", exact=True)
    expect(create).to_be_disabled()
    value.fill("postgres://app:hunter2@db/app")
    expect(value).to_have_attribute("type", "password")
    dialog.get_by_role("button", name="Show what you typed", exact=True).click()
    expect(value).to_have_attribute("type", "text")
    with expect_rpc(
        page, "SetSecrets", lambda body: body.get("environmentId") == production
    ) as saved:
        create.click()
    assert saved.value.ok, saved.value.text()
    assert "resourceId" not in saved.value.request.post_data_json
    expect(dialog).to_have_count(0)
    row = page.get_by_role("row").filter(has_text="DATABASE_URL")
    expect(row).to_contain_text("Not used")
    expect(row).to_contain_text("v1")
    expect(row).to_contain_text("today")
    assert "hunter2" not in stored("DATABASE_URL")
    expect(
        page.get_by_text(
            "Sealed when you save, and never shown again. Reference one with ${{ secrets.NAME }}."
        )
    ).to_be_visible()

    page.get_by_role("button", name="New secret", exact=True).click()
    dialog = page.get_by_role("dialog", name="New secret in production")
    dialog.get_by_role("textbox", name="Name", exact=True).fill("DATABASE_URL")
    expect(
        dialog.get_by_text("DATABASE_URL already exists in production.", exact=True)
    ).to_be_visible()
    dialog.get_by_role("button", name="Cancel", exact=True).click()

    sql(
        state,
        f"""UPDATE deployments SET secret_env={evidence.sql_literal(json.dumps({"DATABASE_URL": "${{ secrets.DATABASE_URL }}"}))}::jsonb
WHERE resource_id='{resource}';""",
    )
    page.reload()
    expect(row.get_by_role("link", name=name, exact=True)).to_have_attribute(
        "href",
        re.compile(rf"/resource/{resource}\?env=production&tab=settings#set-env$"),
    )

    row.get_by_role("button", name="Actions for DATABASE_URL", exact=True).click()
    page.get_by_role("menuitem", name="Copy reference", exact=True).click()
    assert copied_text(page) == "${{ secrets.DATABASE_URL }}"
    row.get_by_role("button", name="Actions for DATABASE_URL", exact=True).click()
    page.get_by_role("menuitem", name="Update value", exact=True).click()
    dialog = page.get_by_role("dialog", name="Update DATABASE_URL")
    expect(
        dialog.get_by_text(
            f"{name} gets the new value on its next deploy or restart.", exact=True
        )
    ).to_be_visible()
    dialog.get_by_label("Value", exact=True).fill("postgres://app:rotated@db/app")
    with expect_rpc(page, "SetSecrets", lambda body: "DATABASE_URL" in body["values"]):
        dialog.get_by_role("button", name="Update", exact=True).click()
    expect(row).to_contain_text("v2")

    page.get_by_role("button", name="Import .env", exact=True).click()
    dialog = page.get_by_role("dialog", name="Import .env into production")
    text = dialog.get_by_role("textbox", name=".env contents")
    text.fill("lower=1")
    expect(
        dialog.get_by_text("Not valid secret names: lower", exact=True)
    ).to_be_visible()
    text.fill("# billing\nSTRIPE_KEY=sk_test\nDATABASE_URL=postgres://app:third@db/app")
    expect(
        dialog.get_by_text("2 found, 1 replace existing", exact=True)
    ).to_be_visible()
    with expect_rpc(
        page,
        "SetSecrets",
        lambda body: sorted(body["values"]) == ["DATABASE_URL", "STRIPE_KEY"],
    ):
        dialog.get_by_role("button", name="Import 2", exact=True).click()
    expect(row).to_contain_text("v3")
    expect(page.get_by_role("row").filter(has_text="STRIPE_KEY")).to_contain_text("v1")

    search = page.get_by_role("textbox", name="Search secrets", exact=True)
    search.fill(name)
    expect(page).to_have_url(re.compile(rf"[?&]q={name}(&|$)"))
    page.reload()
    expect(search).to_have_value(name)
    expect(page.get_by_role("row").filter(has_text="STRIPE_KEY")).to_have_count(0)
    expect(row).to_be_visible()
    search.fill("nothing-here")
    expect(page.get_by_text("No secrets match.", exact=True)).to_be_visible()
    search.fill("")
    expect(page).not_to_have_url(re.compile(r"[?&]q="))

    row.get_by_role("button", name="Actions for DATABASE_URL", exact=True).click()
    blocked = page.get_by_role("menuitem", name=re.compile(r"^Delete"))
    expect(blocked).to_be_disabled()
    expect(blocked).to_contain_text(
        f"Read by {name}. Remove the reference and redeploy first."
    )
    call(
        state,
        secrets_service + "SetSecrets",
        {"environmentId": production, "values": {"STRIPE_KEY": "sk_rotated"}},
    )
    with expect_rpc(
        page, "ListSecrets", lambda body: body.get("environmentId") == production
    ):
        page.evaluate(
            "document.dispatchEvent(new Event('visibilitychange', { bubbles: true }))"
        )
    expect(page.get_by_role("row").filter(has_text="STRIPE_KEY")).to_contain_text("v2")
    expect(blocked).to_be_visible()
    page.keyboard.press("Escape")
    sql(
        state,
        f"UPDATE deployments SET secret_env='{{}}'::jsonb WHERE resource_id='{resource}';",
    )
    page.reload()
    row.get_by_role("button", name="Actions for DATABASE_URL", exact=True).click()
    page.get_by_role("menuitem", name="Delete", exact=True).click()
    confirm = page.get_by_role("alertdialog", name="Delete DATABASE_URL?")
    expect(
        confirm.get_by_text(
            "No resource refers to DATABASE_URL. This can’t be undone.", exact=True
        )
    ).to_be_visible()
    with expect_rpc(
        page, "DeleteSecrets", lambda body: body.get("names") == ["DATABASE_URL"]
    ):
        confirm.get_by_role("button", name="Delete", exact=True).click()
    expect(row).to_have_count(0)
    listed = call(state, secrets_service + "ListSecrets", {"environmentId": production})
    assert [s["name"] for s in listed["secrets"]] == ["STRIPE_KEY"], listed

    assert not errors, errors
    context.close()
print("PASS: secrets")
