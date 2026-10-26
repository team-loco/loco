import json
import os
import shutil
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RELEASE_TASK = ROOT / "mise-tasks/helm/sync/release"
CERT_MANAGER = ROOT / "charts/cert-manager.yaml"
HELM = shutil.which("helm")
BUILD_EGRESS = [{"cidr": "172.30.0.0/16", "port": 5001}]
FIXTURE_ENV = {
    "KIND_CLUSTER": "loco-test",
    "OBS_OTLP_HTTP_PORT": "4318",
    "CLOUDFLARE_TOKEN": "cloudflare-fixture",
    "LOCO_JOIN_TOKEN": "join-fixture",
}
STACK_ENV = ("CERT_MANAGER_EMAIL", *FIXTURE_ENV)
TILT_VALUES = {
    "cert-manager": (),
    "loco-core": ("agent.image.tag=fixture",),
    "loco-operator": ("controller.image.tag=fixture", "builds.builderImage.tag=fixture"),
    "loco-obs": (),
}
PROVIDERS = ("cert-manager",)
CONSUMERS = ("loco-core", "loco-operator", "loco-obs")


def _yaml(path):
    return json.loads(subprocess.check_output(["yq", "-o=json", ".", str(path)], text=True))


def install_args(tmp_path, release, environment=None):
    stubs = tmp_path / "bin"
    stubs.mkdir(exist_ok=True)
    (stubs / "helm").write_text('#!/usr/bin/env bash\nprintf \'%s\\0\' "$@" > "$HELM_ARGS"\n')
    (stubs / "mise").write_text(
        "#!/usr/bin/env bash\n"
        "case $* in\n"
        f"*cluster:build-egress) echo '{json.dumps(BUILD_EGRESS)}' ;;\n"
        "*) exit 1 ;;\n"
        "esac\n"
    )
    for stub in stubs.iterdir():
        stub.chmod(0o755)
    record = tmp_path / "helm-args"
    base = {name: value for name, value in os.environ.items() if name not in STACK_ENV}
    subprocess.run(
        ["bash", str(RELEASE_TASK), release, *TILT_VALUES[release]],
        cwd=ROOT,
        env={
            **base,
            **FIXTURE_ENV,
            **(environment or {}),
            "PATH": f"{stubs}{os.pathsep}{os.environ['PATH']}",
            "HELM_ARGS": str(record),
        },
        check=True,
    )
    return record.read_text().split("\0")[:-1]


def render(args):
    assert args[:2] == ["upgrade", "--install"]
    template = [arg for arg in args[2:] if arg != "--cleanup-on-fail"]
    return subprocess.check_output([HELM, "template", *template], cwd=ROOT, text=True)


def test_providers_wait_and_consumers_do_not(tmp_path, subtests):
    for release in (*PROVIDERS, *CONSUMERS):
        with subtests.test(release=release):
            args = install_args(tmp_path, release)
            assert ("--wait" in args) == (release in PROVIDERS)
            assert "--force-conflicts" in args
            assert args[args.index("--kube-context") + 1] == "kind-" + FIXTURE_ENV["KIND_CLUSTER"]


def test_cert_manager_installs_the_pinned_chart(tmp_path):
    pin = _yaml(CERT_MANAGER)
    args = install_args(tmp_path, "cert-manager")
    assert args[3] == pin["chart"]
    assert args[args.index("--repo") + 1] == pin["repository"]
    assert args[args.index("--version") + 1] == pin["version"]


def test_tilt_values_reach_helm(tmp_path):
    args = install_args(tmp_path, "loco-operator")
    for value in TILT_VALUES["loco-operator"]:
        assert args[args.index(value) - 1] == "--set"


def test_local_releases_render_with_the_stack_values(tmp_path, subtests):
    expected = {
        "loco-core": [FIXTURE_ENV["CLOUDFLARE_TOKEN"], FIXTURE_ENV["LOCO_JOIN_TOKEN"]],
        "loco-operator": [BUILD_EGRESS[0]["cidr"]],
        "loco-obs": ["http://host.docker.internal:" + FIXTURE_ENV["OBS_OTLP_HTTP_PORT"]],
    }
    for release, needles in expected.items():
        with subtests.test(release=release):
            manifest = render(install_args(tmp_path, release))
            for needle in needles:
                assert needle in manifest


def test_cert_manager_email_overrides_the_local_default(tmp_path):
    default = _yaml(ROOT / "env/local/core-chart.yaml")["certManager"]["issuer"]["email"]
    assert default in render(install_args(tmp_path, "loco-core"))
    override = "ops@example.test"
    manifest = render(install_args(tmp_path, "loco-core", {"CERT_MANAGER_EMAIL": override}))
    assert override in manifest
    assert default not in manifest
