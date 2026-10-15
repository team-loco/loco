import subprocess
import unittest

from test_platform_contracts import TEST_PROXY, render, render_chart, render_documents

API_VALUES = {'image': {'tag': 'test'}, 'existingSecret': 'regional-api-env'}


class ControlPlaneInstallation(unittest.TestCase):
    def test_chart_installs_api_and_proxy_without_migration_jobs(self):
        documents = render_chart('loco-control-plane', {'api': API_VALUES, 'obsProxy': TEST_PROXY})
        deployments = {document['metadata']['name'] for document in documents if document['kind'] == 'Deployment'}
        self.assertEqual(deployments, {'loco-control-plane-api', 'loco-control-plane-obs-proxy'})
        self.assertFalse(any(document['kind'] == 'Job' for document in documents))
        routes = [document for document in documents if document['kind'] == 'HTTPRoute']
        self.assertEqual(len(routes), 1)
        self.assertEqual(routes[0]['spec']['rules'][0]['backendRefs'][0]['name'], 'loco-control-plane-api')

    def test_api_uses_existing_secret_and_private_proxy(self):
        deployment = render('loco-control-plane', 'api-deployment.yaml', {
            'api': {**API_VALUES, 'port': 9000, 'env': {'APP_ENV': 'production'}}})
        pod = deployment['spec']['template']['spec']
        self.assertIs(pod['automountServiceAccountToken'], False)
        self.assertNotIn('initContainers', pod)
        api, = pod['containers']
        self.assertEqual(api['envFrom'], [{'secretRef': {'name': 'regional-api-env'}}])
        env = {item['name']: item['value'] for item in api['env']}
        self.assertEqual(env['APP_PORT'], '0.0.0.0:9000')
        self.assertEqual(env['APP_ENV'], 'production')
        self.assertEqual(env['OBSERVABILITY_PROXY_URL'], 'http://loco-control-plane-obs-proxy.platform-test.svc.cluster.local')
        self.assertEqual(api['startupProbe'], {'httpGet': {'path': '/health', 'port': 'http'}, 'failureThreshold': 60, 'periodSeconds': 5})

    def test_api_requires_an_operator_secret(self):
        with self.assertRaises(subprocess.CalledProcessError) as caught:
            render('loco-control-plane', 'api-deployment.yaml', {'api': {'image': {'tag': 'test'}}})
        self.assertIn('api.existingSecret is required', caught.exception.output)

    def test_chart_owns_port_and_proxy_wiring(self):
        deployment = render('loco-control-plane', 'api-deployment.yaml', {
            'api': {**API_VALUES, 'env': {'APP_PORT': 'wrong', 'OBSERVABILITY_PROXY_URL': 'wrong'}}})
        env = deployment['spec']['template']['spec']['containers'][0]['env']
        self.assertEqual([entry['name'] for entry in env], ['APP_PORT', 'OBSERVABILITY_PROXY_URL'])
        self.assertNotIn('wrong', [entry['value'] for entry in env])

    def test_only_api_can_reach_private_proxy(self):
        policy = render('loco-control-plane', 'obs-proxy-network-policy.yaml', {})
        rule, = policy['spec']['ingress']
        self.assertEqual(rule['from'], [{'podSelector': {'matchLabels': {'app': 'loco-control-plane-api'}}}])
        self.assertEqual(rule['ports'], [{'protocol': 'TCP', 'port': 8080}])

    def test_gateway_and_proxy_can_reach_api(self):
        policy = render('loco-control-plane', 'api-network-policy.yaml', {'gatewayNamespace': 'gateway'})
        rule, = policy['spec']['ingress']
        self.assertEqual(rule['from'][0]['namespaceSelector']['matchLabels'], {'kubernetes.io/metadata.name': 'gateway'})
        self.assertEqual(rule['from'][1], {'podSelector': {'matchLabels': {'app': 'loco-control-plane-obs-proxy'}}})

    def test_regional_proxy_can_reach_clickhouse_in_the_obs_namespace(self):
        policies = render_documents('loco-obs', 'network-policies.yaml', {'controlPlaneNamespace': 'regional-cp'})
        clickhouse = next(policy for policy in policies if policy['metadata']['name'] == 'allow-clickhouse-ingress')
        self.assertIn({'from': [{'namespaceSelector': {'matchLabels': {'kubernetes.io/metadata.name': 'regional-cp'}},
                                'podSelector': {'matchLabels': {'app.kubernetes.io/component': 'observability-proxy'}}}],
                       'ports': [{'protocol': 'TCP', 'port': 9000}]}, clickhouse['spec']['ingress'])
