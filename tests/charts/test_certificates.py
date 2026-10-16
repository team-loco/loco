import subprocess

import pytest
from test_platform_contracts import render_documents


def _resources(values):
    return [
        document
        for template in ("cm-issuer.yaml", "cm-cert.yaml", "cloudflare-secret.yml")
        for document in render_documents("loco-core", template, values)
    ]


def test_acme_remains_the_default():
    documents = _resources(
        {
            "certManager": {"issuer": {"email": "operator@example.test"}},
            "secrets": {"cloudflareToken": "cloudflare-token"},
        }
    )
    issuer, certificate, token = documents
    assert issuer["spec"]["acme"]["email"] == "operator@example.test"
    assert certificate["spec"]["issuerRef"] == {
        "name": "letsencrypt-prod",
        "kind": "ClusterIssuer",
    }
    assert token["stringData"]["api-token"] == "cloudflare-token"


def test_self_signed_needs_no_acme_credentials():
    documents = _resources(
        {"certManager": {"issuer": {"name": "local", "type": "selfSigned"}}}
    )
    issuer, certificate = documents
    assert issuer["spec"] == {"selfSigned": {}}
    assert certificate["spec"]["issuerRef"] == {
        "name": "local",
        "kind": "ClusterIssuer",
    }
    assert "namespace" not in issuer["metadata"]


def test_existing_issuer_is_referenced_without_creating_credentials(subtests):
    for kind in ("Issuer", "ClusterIssuer"):
        with subtests.test(kind=kind):
            (certificate,) = _resources(
                {
                    "certManager": {
                        "issuer": {
                            "create": False,
                            "name": "operator-issuer",
                            "kind": kind,
                        }
                    }
                }
            )
            assert certificate["spec"]["issuerRef"] == {
                "name": "operator-issuer",
                "kind": kind,
            }


def test_existing_secret_skips_all_certificate_resources():
    values = {"certManager": {"existingTLSSecret": "operator-tls"}}
    assert _resources(values) == []
    gateway = next(
        (
            document
            for document in render_documents("loco-core", "gateway.yaml", values)
            if document["kind"] == "Gateway"
        )
    )
    listeners = [
        listener
        for listener in gateway["spec"]["listeners"]
        if listener["protocol"] == "HTTPS"
    ]
    assert listeners
    for listener in listeners:
        assert listener["tls"]["certificateRefs"] == [
            {"name": "operator-tls", "kind": "Secret", "group": ""}
        ]


def test_default_gateway_uses_the_issued_secret():
    (certificate,) = render_documents("loco-core", "cm-cert.yaml", {})
    gateway = next(
        (
            document
            for document in render_documents("loco-core", "gateway.yaml", {})
            if document["kind"] == "Gateway"
        )
    )
    for listener in gateway["spec"]["listeners"]:
        if listener["protocol"] == "HTTPS":
            assert (
                listener["tls"]["certificateRefs"][0]["name"]
                == certificate["spec"]["secretName"]
            )


def test_invalid_created_issuer_configuration_fails(subtests):
    for issuer in ({"type": "unknown"}, {"kind": "Issuer"}):
        with subtests.test(issuer=issuer):
            with pytest.raises(subprocess.CalledProcessError):
                _resources({"certManager": {"issuer": issuer}})
