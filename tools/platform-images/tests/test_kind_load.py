import os
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]


@pytest.mark.parametrize("platform", ["linux/amd64", "linux/arm64"])
@pytest.mark.parametrize("failure", ["", "save", "load"])
def test_kind_load_exports_only_node_platform_and_cleans_archive(tmp_path, platform, failure):
    archive = tmp_path / "image.tar"
    script = r'''
set -euo pipefail
source "$LOADER"
mktemp() { printf '%s\n' "$ARCHIVE"; }
docker() {
    [[ $1 == image && $2 == save && $3 == --platform && $4 == "$PLATFORM" ]]
    [[ $5 == --output && $6 == "$ARCHIVE" && $7 == moby/buildkit:test ]]
    printf '%s\n' "$PLATFORM" > "$ARCHIVE"
    [[ $FAILURE != save ]]
}
kind() {
    [[ $1 == load && $2 == image-archive && $3 == "$ARCHIVE" ]]
    [[ $4 == --name && $5 == fixture ]]
    [[ $(cat "$ARCHIVE") == "$PLATFORM" ]]
    touch "$LOADED"
    [[ $FAILURE != load ]]
}
load_kind_image moby/buildkit:test fixture "$PLATFORM"
'''
    loaded = tmp_path / "loaded"
    result = subprocess.run(
        ["bash", "-c", script],
        env={
            **os.environ,
            "LOADER": str(ROOT / "tests/e2e/lib.sh"),
            "ARCHIVE": str(archive),
            "PLATFORM": platform,
            "FAILURE": failure,
            "LOADED": str(loaded),
        },
        capture_output=True,
        text=True,
    )
    assert (result.returncode == 0) == (not failure), result.stderr
    assert loaded.exists() == (failure != "save")
    assert not archive.exists()
