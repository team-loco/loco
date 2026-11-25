import argparse
from pathlib import Path

from harness import load_state, open_page, repo, session, wait_for_query
from playwright.sync_api import expect
from settings_fixture import seed_settings_resource

parser = argparse.ArgumentParser(
    description="Check resource settings deep links and history"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
parser.add_argument("--out", default=str(repo / "tests/ui/out/settings-navigation"))
args = parser.parse_args()
state = load_state(
    args, "settings-navigation", "tests/ui/fixtures/deployment-history/scenario.json"
)
out = Path(args.out)
out.mkdir(parents=True, exist_ok=True)
_, resource, base = seed_settings_resource(state, "nav")
_, _, sized = seed_settings_resource(state, "sized", cpu="256m", memory="64Mi")

with session(out) as browser:
    context, page, errors = open_page(browser, state=state)
    page.goto(base + "?env=production&tab=settings#set-health")
    expect(page.locator("#set-health")).to_be_visible()
    page.wait_for_function(
        "() => { const top = document.querySelector('#set-health').getBoundingClientRect().top; return top >= 140 && top < 150; }"
    )
    expect(
        page.get_by_role("navigation", name="Settings sections").get_by_role(
            "link", name="Health check", exact=True
        )
    ).to_have_attribute("aria-current", "true")

    page.goto(base + "?env=production")
    overview = page.get_by_role("button", name="Overview", exact=True)
    page.get_by_role("button", name="Resource settings", exact=True).click()
    wait_for_query(page, lambda query: query.get("tab") == ["settings"])
    expect(overview).to_be_visible()
    page.go_back()
    wait_for_query(page, lambda query: "tab" not in query)
    expect(overview).to_have_count(0)
    page.go_forward()
    wait_for_query(page, lambda query: query.get("tab") == ["settings"])
    expect(overview).to_be_visible()
    overview.click()
    wait_for_query(page, lambda query: query.get("tab") in (None, ["overview"]))
    page.go_back()
    wait_for_query(page, lambda query: query.get("tab") == ["settings"])

    page.goto(base + "?env=staging&range=zzz&tab=nope")
    expect(
        page.get_by_role("button", name="Resource settings", exact=True)
    ).to_be_visible()
    wait_for_query(
        page,
        lambda query: (
            query.get("env") == ["production"]
            and "range" not in query
            and "tab" not in query
        ),
    )

    page.goto(base + "?env=production&tab=settings")
    nav = page.get_by_role("navigation", name="Settings sections")
    expect(nav.get_by_role("link", name="Image", exact=True)).to_have_attribute(
        "aria-current", "true"
    )
    # Connection variables load late and push every section below them down.
    expect(page.get_by_text("Connection variables", exact=True)).to_be_visible()
    page.evaluate("document.querySelector('#set-health').scrollIntoView()")
    expect(nav.get_by_role("link", name="Health check", exact=True)).to_have_attribute(
        "aria-current", "true"
    )
    expect(nav.get_by_role("link", name="Image", exact=True)).not_to_have_attribute(
        "aria-current", "true"
    )
    page.evaluate("document.querySelector('#set-scaling').scrollIntoView()")
    expect(nav.get_by_role("link", name="Scaling", exact=True)).to_have_attribute(
        "aria-current", "true"
    )

    page.evaluate("window.scrollTo(0, 0)")
    expect(nav.get_by_role("link", name="Image", exact=True)).to_have_attribute(
        "aria-current", "true"
    )
    page.evaluate(
        """() => {
            window.lit = [];
            new MutationObserver((records) => {
                for (const r of records)
                    if (r.target.getAttribute('aria-current') === 'true') window.lit.push(r.target.textContent);
            }).observe(document.querySelector('nav[aria-label="Settings sections"]'),
                { attributes: true, subtree: true, attributeFilter: ['aria-current'] });
        }"""
    )
    nav.get_by_role("link", name="Variables", exact=True).click()
    page.wait_for_function(
        "() => { const top = document.querySelector('#set-env').getBoundingClientRect().top; return top >= 140 && top < 150; }"
    )
    # The page scrolls past other sections on the way; only the clicked item lights up.
    assert page.evaluate("window.lit") == ["Variables"], page.evaluate("window.lit")
    expect(nav.get_by_role("link", name="Variables", exact=True)).to_have_attribute(
        "aria-current", "true"
    )

    page.goto(sized + "?env=production&tab=settings#set-size")
    cpu = page.get_by_role("slider", name="CPU", exact=True)
    memory = page.get_by_role("slider", name="Memory", exact=True)
    expect(cpu).to_have_attribute("aria-valuetext", "0.256 vCPU")
    expect(memory).to_have_attribute("aria-valuetext", "64 Mi")
    expect(cpu).to_have_attribute("aria-valuenow", "2")
    expect(memory).to_have_attribute("aria-valuenow", "0")
    assert not errors, errors
    context.close()
    browser.close()
print("PASS: settings deep links scroll to their section and the header keeps history")
