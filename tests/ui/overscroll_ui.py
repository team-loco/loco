import argparse

from harness import load_state, open_page, repo, session

parser = argparse.ArgumentParser(
    description="Check the document canvas matches the app shell in every theme"
)
parser.add_argument("--state")
parser.add_argument("--keep", action="store_true")
args = parser.parse_args()
state = load_state(args, "overscroll", "tests/ui/fixtures/audit/scenario.json")
THEMES = ["loco", "catppuccin", "rose-pine", "solarized", "neobrutal", "paper"]

with session(repo / "tests/ui/out/overscroll") as browser:
    for mode in ["light", "dark"]:
        for theme in THEMES:
            context, page, errors = open_page(
                browser,
                state=state,
                storage={"loco:mode:v1": mode, "loco:color:v1": theme},
            )
            page.goto(state["ui_url"] + "/tokens?owner=organization")
            page.locator('[data-slot="sidebar-wrapper"]').wait_for()
            colors = page.evaluate(
                """() => ({
                    html: getComputedStyle(document.documentElement).backgroundColor,
                    shell: getComputedStyle(document.querySelector('[data-slot="sidebar-wrapper"]')).backgroundColor,
                })"""
            )
            assert colors["html"] == colors["shell"], (theme, mode, colors)
            assert not errors, errors
            context.close()
