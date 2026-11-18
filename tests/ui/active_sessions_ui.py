import argparse

from harness import evidence, load_state, open_page, repo, session
from playwright.sync_api import expect

SCENARIO = "tests/ui/fixtures/deployment-history/scenario.json"
USER_SERVICE = "loco.user.v1.UserService/"
LIST_SESSIONS = "**/" + USER_SERVICE + "ListSessions"
REVOKE_SESSION = "**/" + USER_SERVICE + "RevokeSession"


def seed_sessions(state):
    owner = state["users"]["owner"]
    cli = evidence.session_token()
    psql = evidence.sql_literal
    evidence.psql(
        state["container"],
        f"""UPDATE session_tokens
            SET user_agent = 'Mozilla/5.0 (Macintosh; Intel Mac OS X) Chrome/135.0.0.0'
            WHERE access_token_hash = {psql(evidence.token_hash(owner["token"]))};
        INSERT INTO session_tokens (
            access_token_hash, refresh_token_hash, user_id,
            access_expires_at, refresh_expires_at, kind, created_at, last_used_at
        ) VALUES (
            {psql(evidence.token_hash(cli))}, {psql(evidence.token_hash("refresh-" + cli))},
            {psql(owner["id"])}, NOW() + INTERVAL '1 hour', NOW() + INTERVAL '1 day',
            'cli', NOW() - INTERVAL '1 day', NOW() - INTERVAL '1 hour'
        );""",
    )
    return cli


def verify(state):
    cli = seed_sessions(state)
    owner = state["users"]["owner"]
    list_result = evidence.rpc(
        state["api_url"], owner["token"], USER_SERVICE + "ListSessions", {}
    )
    cli_id = next(
        row["id"]
        for row in list_result["sessions"]
        if row["kind"] == "SESSION_KIND_CLI"
    )

    with session(repo / "tests/ui/out/active-sessions") as browser:
        context, page, errors = open_page(browser, state=state, narrow=True)
        context.route(
            LIST_SESSIONS,
            lambda route: route.fulfill(
                status=503,
                json={"code": "unavailable", "message": "Session list unavailable"},
            ),
        )
        page.goto(state["ui_url"] + "/profile")
        expect(
            page.get_by_text("Session list unavailable.", exact=True)
        ).to_be_visible()

        context.unroute(LIST_SESSIONS)
        page.get_by_role("button", name="Retry", exact=True).click()
        sessions = page.get_by_role("list", name="Active sessions", exact=True)
        current = sessions.get_by_role("listitem").first
        expect(current.get_by_text("Chrome on macOS", exact=True)).to_be_visible()
        expect(current.get_by_text("This browser", exact=True)).to_be_visible()
        expect(current.get_by_role("button", name="Sign out")).to_have_count(0)
        expect(sessions.get_by_text("Loco CLI", exact=True)).to_be_visible()
        assert not page.evaluate(
            """id => JSON.stringify(Object.values(localStorage)).includes(id)""", cli_id
        )

        sign_out = sessions.get_by_role("button", name="Sign out Loco CLI session")
        sign_out.click()
        confirmation = page.get_by_role(
            "alertdialog", name="Sign out this session?", exact=True
        )
        confirmation.get_by_role("button", name="Cancel", exact=True).click()
        expect(sign_out).to_be_visible()
        assert (
            evidence.rpc(state["api_url"], cli, USER_SERVICE + "WhoAmI", {})["user"][
                "id"
            ]
            == owner["id"]
        )

        sign_out.click()
        context.route(
            REVOKE_SESSION,
            lambda route: route.fulfill(
                status=503,
                json={"code": "unavailable", "message": "Try signing out again"},
            ),
        )
        confirm = confirmation.get_by_role("button", name="Sign out", exact=True)
        confirm.click()
        expect(confirmation.get_by_role("alert")).to_have_text("Try signing out again.")
        context.unroute(REVOKE_SESSION)
        confirm.click()
        expect(confirmation).to_be_hidden()
        expect(sessions.get_by_text("Loco CLI", exact=True)).to_have_count(0)
        expect(current.get_by_text("This browser", exact=True)).to_be_visible()
        try:
            evidence.rpc(state["api_url"], cli, USER_SERVICE + "WhoAmI", {})
        except evidence.RPCError as error:
            assert error.code == "unauthenticated", error
        else:
            raise AssertionError("The signed-out CLI session still authenticates")
        assert not errors, errors
        context.close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="List and revoke active sessions")
    parser.add_argument("--state")
    args = parser.parse_args()
    state = load_state(args, "active-sessions", SCENARIO)
    verify(state)
    print("Active sessions: current browser, remote sign-out and error recovery passed")
