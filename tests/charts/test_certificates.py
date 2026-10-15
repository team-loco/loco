import subprocess
import unittest

from test_platform_contracts import render_documents


class GatewayCertificates(unittest.TestCase):
    def resources(self, values):
        return [document for template in ('cm-issuer.yaml', 'cm-cert.yaml', 'cloudflare-secret.yml')
                for document in render_documents('loco-core', template, values)]

    def test_acme_remains_the_default(self):
        documents = self.resources({'certManager': {'issuer': {'email': 'operator@example.test'}},
                                    'secrets': {'cloudflareToken': 'cloudflare-token'}})
        issuer, certificate, token = documents
        self.assertEqual(issuer['spec']['acme']['email'], 'operator@example.test')
        self.assertEqual(certificate['spec']['issuerRef'], {'name': 'letsencrypt-prod', 'kind': 'ClusterIssuer'})
        self.assertEqual(token['stringData']['api-token'], 'cloudflare-token')

    def test_self_signed_needs_no_acme_credentials(self):
        documents = self.resources({'certManager': {'issuer': {'name': 'local', 'type': 'selfSigned'}}})
        issuer, certificate = documents
        self.assertEqual(issuer['spec'], {'selfSigned': {}})
        self.assertEqual(certificate['spec']['issuerRef'], {'name': 'local', 'kind': 'ClusterIssuer'})
        self.assertNotIn('namespace', issuer['metadata'])

    def test_existing_issuer_is_referenced_without_creating_credentials(self):
        for kind in ('Issuer', 'ClusterIssuer'):
            with self.subTest(kind=kind):
                certificate, = self.resources({'certManager': {'issuer': {'create': False, 'name': 'operator-issuer', 'kind': kind}}})
                self.assertEqual(certificate['spec']['issuerRef'], {'name': 'operator-issuer', 'kind': kind})

    def test_existing_secret_skips_all_certificate_resources(self):
        values = {'certManager': {'existingTLSSecret': 'operator-tls'}}
        self.assertEqual(self.resources(values), [])
        gateway = next(document for document in render_documents('loco-core', 'gateway.yaml', values)
                       if document['kind'] == 'Gateway')
        listeners = [listener for listener in gateway['spec']['listeners'] if listener['protocol'] == 'HTTPS']
        self.assertTrue(listeners)
        for listener in listeners:
            self.assertEqual(listener['tls']['certificateRefs'], [{'name': 'operator-tls', 'kind': 'Secret', 'group': ''}])

    def test_default_gateway_uses_the_issued_secret(self):
        certificate, = render_documents('loco-core', 'cm-cert.yaml', {})
        gateway = next(document for document in render_documents('loco-core', 'gateway.yaml', {})
                       if document['kind'] == 'Gateway')
        for listener in gateway['spec']['listeners']:
            if listener['protocol'] == 'HTTPS':
                self.assertEqual(listener['tls']['certificateRefs'][0]['name'], certificate['spec']['secretName'])

    def test_invalid_created_issuer_configuration_fails(self):
        for issuer in ({'type': 'unknown'}, {'kind': 'Issuer'}):
            with self.subTest(issuer=issuer):
                with self.assertRaises(subprocess.CalledProcessError):
                    self.resources({'certManager': {'issuer': issuer}})
