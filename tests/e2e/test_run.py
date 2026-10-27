import subprocess
from pathlib import Path

import pytest

LIB = Path(__file__).resolve().parent / "lib.sh"


def selected(name, *filters):
    result = subprocess.run(
        ["bash", "-c", 'source "$1"; shift; suite_selected "$@"', "suite_selected", str(LIB), name, *filters],
        check=False,
    )
    return result.returncode == 0


def test_no_filter_selects_every_suite():
    assert selected("03_builds")


@pytest.mark.parametrize(
    ("name", "filters", "expected"),
    [
        ("03_builds", ["build"], True),
        ("06_builds_disabled", ["build"], True),
        ("06_builds_disabled", ["03_builds"], False),
        ("04_build_dispatch", ["04_build_dispatch", "05_cli_build_deploy"], True),
        ("05_cli_build_deploy", ["04_build_dispatch", "05_cli_build_deploy"], True),
        ("03_builds", ["04_build_dispatch", "05_cli_build_deploy"], False),
    ],
)
def test_filters_select_suites_whose_name_contains_any(name, filters, expected):
    assert selected(name, *filters) is expected
