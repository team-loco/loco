import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
COLLECTOR_CONFIGMAPS = ('otel-col-daemon-agent', 'otel-col-deploy', 'otel-col-cluster')
PUSH_RECEIVERS = {'otlp', 'jaeger', 'zipkin'}
TENANT_LABELS = {
    'loco.io/organization-id': 'loco.organization.id',
    'loco.io/workspace-id': 'loco.workspace.id',
    'loco.io/environment-id': 'loco.environment.id',
    'loco.io/resource-id': 'loco.resource.id',
}
BUILD_LABELS = {'loco.io/build-id': 'loco.io/build-id'}
GATEWAY_LABELS = {
    'gateway.envoyproxy.io/owning-gateway-name': 'gateway.envoyproxy.io/owning-gateway-name',
    'gateway.envoyproxy.io/owning-gateway-namespace': 'gateway.envoyproxy.io/owning-gateway-namespace',
}
CLIENT_POD_IDENTITY = ('k8s.pod.ip', 'k8s.pod.uid')
ENVOY_CLUSTER_ATTRIBUTES = {'trace_statements': 'upstream_cluster', 'log_statements': 'upstream_cluster', 'metric_statements': 'envoy.cluster_name'}
ROUTE_TENANCY = ('loco.workspace.id', 'loco.resource.id')
TEST_PROXY = {'image': {'tag': 'test'}, 'controlPlane': {'url': 'https://api.example.test'},
              'clickhouse': {'existingSecret': {'name': 'proxy-reader', 'key': 'url'}, 'migratorExistingSecret': {'name': 'proxy-migrator', 'key': 'url'}}}
TEST_REGION = 'test-region-1'
OBS_BASE_VALUES = {'global': {'region': TEST_REGION}}
CLICKHOUSE_USER_PASSWORDS = {'CLICKHOUSE_MIGRATOR_PASSWORD': 'test', 'CLICKHOUSE_INGEST_PASSWORD': 'test', 'CLICKHOUSE_READER_PASSWORD': 'test'}


def environment_values(environment, variables, chart='loco-obs'):
    with tempfile.TemporaryDirectory() as directory:
        state = Path(directory) / 'helmfile.yaml'
        config = json.dumps({'releases': [{
            'name': chart, 'chart': str(ROOT / 'charts' / chart),
            'values': [str(ROOT / 'env' / environment / (chart.removeprefix('loco-') + '-chart.yaml.gotmpl'))],
        }]})
        state.write_text(subprocess.check_output(['yq', '-P', '.', '-'], input=config, text=True))
        built = subprocess.check_output(['helmfile', '-f', str(state), 'build', '--embed-values'],
                                        env={**os.environ, **variables}, text=True)
        document = subprocess.check_output(['yq', '-o=json', '.', '-'], input=built, text=True)
        return json.loads(document)['releases'][0]['values'][0]


def render(chart, template, overrides):
    documents = render_documents(chart, template, overrides)
    return documents[0] if documents else None


def chart_values(chart):
    return json.loads(subprocess.check_output(['yq', '-o=json', '.', str(ROOT / 'charts' / chart / 'values.yaml')], text=True))


def render_documents(chart, template, overrides):
    source = ROOT / 'charts' / chart
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        (root / 'templates').mkdir()
        metadata = subprocess.check_output(['yq', '-o=json', '.', str(source / 'Chart.yaml')], text=True)
        chart_values = json.loads(metadata)
        chart_values.pop('dependencies', None)
        (root / 'Chart.yaml').write_text(json.dumps(chart_values))
        shutil.copyfile(source / 'values.yaml', root / 'values.yaml')
        target = root / 'templates' / template
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source / 'templates' / template, target)
        for helper in (source / 'templates').glob('*.tpl'):
            shutil.copyfile(helper, root / 'templates' / helper.name)
        (root / 'overrides.json').write_text(json.dumps(overrides))
        rendered = subprocess.check_output(['helm', 'template', chart, str(root), '-n', 'platform-test', '-f', str(root / 'overrides.json')], text=True, stderr=subprocess.STDOUT)
        documents = subprocess.check_output(['yq', '-o=json', '-I=0', '.', '-'], input=rendered, text=True)
        return [json.loads(line) for line in documents.splitlines() if line.strip() not in ('', 'null')]


def render_chart(chart, overrides, check=True):
    with tempfile.TemporaryDirectory() as directory:
        base = Path(directory) / 'base.json'
        base.write_text(json.dumps(OBS_BASE_VALUES if chart == 'loco-obs' else {}))
        values = Path(directory) / 'overrides.json'
        values.write_text(json.dumps(overrides))
        command = ['helm', 'template', chart, str(ROOT / 'charts' / chart), '-n', 'platform-test', '-f', str(base), '-f', str(values)]
        result = subprocess.run(command, capture_output=True, text=True)
        if not check:
            return result
        if result.returncode != 0:
            raise subprocess.CalledProcessError(result.returncode, command, result.stdout, result.stderr)
        rendered = result.stdout
    documents = subprocess.check_output(['yq', '-o=json', '-I=0', '.', '-'], input=rendered, text=True)
    return [json.loads(line) for line in documents.splitlines() if line.strip() not in ('', 'null')]


QUANTITY_SUFFIXES = {'m': 0.001, 'Mi': 2 ** 20, 'Gi': 2 ** 30}


def quantity(value):
    for suffix, scale in QUANTITY_SUFFIXES.items():
        if value.endswith(suffix):
            return float(value.removesuffix(suffix)) * scale
    return float(value)


def clickhouse_installation(overrides):
    for document in render_chart('loco-obs', overrides):
        if document['kind'] == 'ClickHouseInstallation':
            return document
    raise AssertionError('loco-obs renders no ClickHouseInstallation')


def clickhouse_resources(overrides):
    containers = clickhouse_installation(overrides)['spec']['templates']['podTemplates'][0]['spec']['containers']
    return next(container['resources'] for container in containers if container['name'] == 'clickhouse')


def local_obs_values():
    return environment_values('local', {'APP_PORT': '8080'})


def collector_relays(documents):
    configs = {}
    for document in documents:
        if document['kind'] == 'ConfigMap' and document['metadata']['name'] in COLLECTOR_CONFIGMAPS:
            relay = subprocess.check_output(['yq', '-o=json', '-I=0', '.', '-'], input=document['data']['relay'], text=True)
            configs[document['metadata']['name']] = json.loads(relay)
    return configs


class PlatformContracts(unittest.TestCase):
    def test_regional_certificate_names(self):
        certificate = render('loco-core', 'cm-cert.yaml', {
            'global': {'domain': {'apps': ['apps.example.test'], 'platform': 'platform.example.test'}},
            'certManager': {'additionalDNSNames': ['*.staging-east.apps.example.test', '*.staging-west.apps.example.test']},
        })
        self.assertEqual(certificate['spec']['dnsNames'], [
            '*.apps.example.test', '*.platform.example.test',
            '*.staging-east.apps.example.test', '*.staging-west.apps.example.test',
        ])

    def test_database_and_auth_secret_references(self):
        deployment = render('loco-control-plane', 'obs-proxy-deployment.yaml', {'obsProxy': TEST_PROXY})
        env = {item['name']: item for item in deployment['spec']['template']['spec']['containers'][0]['env']}
        self.assertEqual(env['CLICKHOUSE_URL']['valueFrom']['secretKeyRef'], {'name': 'proxy-reader', 'key': 'url'})
        self.assertEqual(env['CLICKHOUSE_MIGRATOR_URL']['valueFrom']['secretKeyRef'], {'name': 'proxy-migrator', 'key': 'url'})
        self.assertNotIn('PROXY_AUTH_TOKEN', env)

    def test_missing_clickhouse_secrets_are_rejected(self):
        for secret in ('existingSecret', 'migratorExistingSecret'):
            with self.subTest(secret=secret):
                clickhouse = {**TEST_PROXY['clickhouse'], secret: {'name': '', 'key': 'url'}}
                with self.assertRaises(subprocess.CalledProcessError) as caught:
                    render('loco-control-plane', 'obs-proxy-deployment.yaml', {'obsProxy': {**TEST_PROXY, 'clickhouse': clickhouse}})
                self.assertIn(f'obsProxy.clickhouse.{secret}.name is required', caught.exception.output)

    def test_obs_chart_runs_no_proxy(self):
        names = {document['metadata']['name'] for document in render_chart('loco-obs', {})}
        self.assertFalse({name for name in names if 'obs-proxy' in name})

    def test_missing_database_secret_key_is_rejected(self):
        with self.assertRaises(subprocess.CalledProcessError):
            render('loco-control-plane', 'obs-proxy-deployment.yaml', {'obsProxy': {**TEST_PROXY, 'clickhouse': {**TEST_PROXY['clickhouse'], 'existingSecret': {'name': 'db', 'key': ''}}}})

    def test_missing_permission_endpoint_is_rejected(self):
        with self.assertRaises(subprocess.CalledProcessError) as caught:
            render('loco-control-plane', 'obs-proxy-deployment.yaml', {'obsProxy': {'image': {'tag': 'test'}}})
        self.assertIn('obsProxy.controlPlane.url is required', caught.exception.output)


class ObservabilitySchema(unittest.TestCase):
    def proxy_env(self, overrides=None):
        values = {'obsProxy': dict(TEST_PROXY)}
        for key, value in (overrides or {}).items():
            values['obsProxy'][key] = value
        deployment = render('loco-control-plane', 'obs-proxy-deployment.yaml', values)
        container = deployment['spec']['template']['spec']['containers'][0]
        return container, [item['name'] for item in container['env']], {item['name']: item for item in container['env']}

    def test_proxy_cors_origins_are_explicit(self):
        _, _, env = self.proxy_env({'cors': {'allowedOrigins': ['https://app.example.test', 'http://localhost:5173']}})
        self.assertEqual(env['CORS_ALLOWED_ORIGINS']['value'], 'https://app.example.test,http://localhost:5173')
        _, _, env = self.proxy_env()
        self.assertEqual(env['CORS_ALLOWED_ORIGINS']['value'], '')

    def test_proxy_uses_its_built_in_schema_settings(self):
        container, _, env = self.proxy_env()
        built_in = {'CLICKHOUSE_DB', 'CLICKHOUSE_LOGS_TTL', 'CLICKHOUSE_TRACES_TTL', 'CLICKHOUSE_METRICS_TTL',
                    'MIGRATION_RETRY_BUDGET', 'MIGRATION_RETRY_INTERVAL', 'MIGRATION_LOCK_TTL', 'MIGRATION_LOCK_RETRY_PERIOD'}
        self.assertFalse(built_in & set(env), 'the chart sets settings the proxy defaults itself')
        self.assertNotIn('PERMISSION_CHECK_TIMEOUT', env)
        self.assertEqual(container['readinessProbe']['httpGet']['path'], '/readyz')
        self.assertNotIn('startupProbe', container)

    def test_user_secrets_follow_the_clickhouse_users(self):
        passwords = {'loco_migrator': 'migrator-pw', 'loco_ingest': 'ingest-pw', 'loco_reader': 'reader-pw'}
        secrets = render_documents('loco-obs', 'clickhouse-users.yaml', {'clickhouseUserPasswords': passwords})
        found = {(item['metadata']['namespace'], item['metadata']['name']): item['stringData']['password'] for item in secrets}
        self.assertEqual(found, {
            ('platform-test', 'loco-obs-clickhouse-migrator'): 'migrator-pw',
            ('platform-test', 'loco-obs-clickhouse-ingest'): 'ingest-pw',
            ('platform-test', 'loco-obs-clickhouse-reader'): 'reader-pw',
            ('observability-node', 'loco-obs-clickhouse-ingest'): 'ingest-pw',
        })

    def test_user_secrets_are_left_to_the_cluster_without_passwords(self):
        self.assertEqual(render_documents('loco-obs', 'clickhouse-users.yaml', {}), [])

    def test_passwords_that_break_a_dsn_are_rejected(self):
        with self.assertRaises(subprocess.CalledProcessError):
            render_documents('loco-obs', 'clickhouse-users.yaml', {'clickhouseUserPasswords': {'loco_reader': 'a b@c'}})

    def test_clickhouse_has_requests_and_limits(self):
        resources = clickhouse_resources({})
        for kind in ('requests', 'limits'):
            self.assertEqual(set(resources[kind]), {'cpu', 'memory'}, kind)
        self.assertEqual(resources['requests']['memory'], resources['limits']['memory'])

    def test_local_clickhouse_is_smaller_than_the_default(self):
        default = clickhouse_resources({})
        local = clickhouse_resources(local_obs_values())
        for kind in ('requests', 'limits'):
            for resource in ('cpu', 'memory'):
                self.assertLess(quantity(local[kind][resource]), quantity(default[kind][resource]), f'{kind}.{resource}')

    def test_local_clickhouse_caps_its_caches_and_schedule_pool(self):
        capped = {'mark_cache_size', 'index_mark_cache_size', 'primary_index_cache_size', 'uncompressed_cache_size', 'background_schedule_pool_size', 'max_concurrent_queries'}
        local = clickhouse_installation(local_obs_values())['spec']['configuration'].get('settings', {})
        default = clickhouse_installation({})['spec']['configuration'].get('settings', {})
        self.assertEqual(capped - set(local), set())
        for setting in capped:
            self.assertRegex(str(local[setting]), r'^[0-9]+$', setting)
        self.assertEqual(capped & set(default), set())

    def test_in_cluster_clickhouse_is_the_default_endpoint(self):
        documents = render_chart('loco-obs', {})
        host = 'clickhouse-loco-obs-clickhouse.platform-test.svc.cluster.local:9000'
        for collector, config in collector_relays(documents).items():
            exporter = config['exporters']['clickhouse']
            self.assertEqual(exporter['endpoint'], f'tcp://{host}', collector)
            self.assertEqual(exporter['connection_params']['secure'], 'false', collector)
        self.assertIn('ClickHouseInstallation', {document['kind'] for document in documents})

    def test_external_clickhouse_replaces_the_in_cluster_server(self):
        overrides = {'clickhouse': {'enabled': False}, 'global': {'clickhouse': {'host': 'abc.clickhouse.example.test', 'port': 9440, 'secure': True}}}
        documents = render_chart('loco-obs', overrides)
        host = 'abc.clickhouse.example.test:9440'
        for collector, config in collector_relays(documents).items():
            exporter = config['exporters']['clickhouse']
            self.assertEqual(exporter['endpoint'], f'tcp://{host}', collector)
            self.assertEqual(exporter['connection_params']['secure'], 'true', collector)
        names = {document['metadata']['name'] for document in documents}
        kinds = {document['kind'] for document in documents}
        self.assertNotIn('ClickHouseInstallation', kinds)
        self.assertNotIn('allow-clickhouse-ingress', names)

    def test_collectors_set_connection_options_as_exporter_fields(self):
        for collector, config in collector_relays(render_chart('loco-obs', {})).items():
            exporter = config['exporters']['clickhouse']
            self.assertNotIn('?', exporter['endpoint'], collector)
            self.assertEqual(exporter['compress'], 'lz4', collector)
            self.assertTrue(exporter['async_insert'], collector)
            self.assertEqual(exporter['connection_params'], {'dial_timeout': '10s', 'log_queries': '0', 'secure': 'false'}, collector)

    def test_external_clickhouse_needs_a_host(self):
        with self.assertRaises(subprocess.CalledProcessError):
            render_chart('loco-obs', {'clickhouse': {'enabled': False}})

    def test_collectors_ingest_into_the_database_the_users_are_granted(self):
        values = chart_values('loco-obs')
        database = values['otel-col-daemon']['config']['exporters']['clickhouse']['database']
        users = {user['name']: user for user in values['clickhouse']['clickhouse']['users']}
        for user in users.values():
            self.assertTrue(all(grant.endswith(f' ON {database}.*') for grant in user['grants']), user['name'])
        for collector in ('otel-col-daemon', 'otel-col-deploy'):
            config = values[collector]['config']
            exporter = config['exporters']['clickhouse']
            self.assertFalse(exporter['create_schema'], collector)
            self.assertEqual(exporter['database'], database, collector)
            self.assertNotIn('ttl', exporter, collector)
            self.assertEqual(config['service']['pipelines']['traces']['exporters'], ['clickhouse'], collector)
            user = users[exporter['username']]
            env = {item['name']: item for item in values[collector]['extraEnvs']}
            self.assertEqual(env['CLICKHOUSE_INGEST_PASSWORD']['valueFrom']['secretKeyRef'], {'name': user['password_secret_name'], 'key': 'password'}, collector)
            self.assertEqual(exporter['password'], '${env:CLICKHOUSE_INGEST_PASSWORD}', collector)
        for user in users.values():
            for grant in user['grants']:
                self.assertIn(f' ON {database}.*', grant, user['name'])


class CollectorTenancy(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.documents = render_chart('loco-obs', {})
        cls.configs = collector_relays(cls.documents)

    def pipelines(self):
        for collector, config in self.configs.items():
            for name, pipeline in config['service']['pipelines'].items():
                yield collector, config, name, pipeline

    def push_pipelines(self):
        for collector, config, name, pipeline in self.pipelines():
            if PUSH_RECEIVERS & set(pipeline['receivers']):
                yield collector, config, name, pipeline

    def k8s_attributes(self, pipeline):
        return [processor for processor in pipeline.get('processors', []) if processor.split('/')[0] == 'k8s_attributes']

    def test_all_collectors_render(self):
        self.assertEqual(set(self.configs), set(COLLECTOR_CONFIGMAPS))

    def test_every_pipeline_stamps_the_region_last(self):
        for collector, config, name, pipeline in self.pipelines():
            with self.subTest(collector=collector, pipeline=name):
                self.assertEqual(pipeline['processors'][-2:], ['resource/region', 'batch'])
                self.assertEqual(config['processors']['resource/region'], {'attributes': [
                    {'key': 'loco.region', 'value': TEST_REGION, 'action': 'upsert'}]})

    def test_chart_requires_a_region(self):
        result = render_chart('loco-obs', {'global': {'region': ''}}, check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('global.region is required', result.stderr)

    def test_every_environment_sets_a_region(self):
        local = environment_values('local', {'APP_PORT': '8080'})
        self.assertTrue(local['global']['region'])
        prod = environment_values('prod', {
            'GH_SHA': 'test', 'CLICKHOUSE_PASSWORD': 'test',
            'CONTROL_PLANE_URL': 'https://api.example.test', 'LOCO_REGION': TEST_REGION, **CLICKHOUSE_USER_PASSWORDS})
        self.assertEqual(prod['global']['region'], TEST_REGION)

    def test_collectors_run_without_an_operator(self):
        names = {document['metadata'].get('labels', {}).get('app.kubernetes.io/name') for document in self.documents}
        kinds = {document['kind'] for document in self.documents}
        self.assertNotIn('opentelemetry-operator', names)
        self.assertFalse({'MutatingWebhookConfiguration', 'ValidatingWebhookConfiguration'} & kinds)

    def test_memory_limiter_runs_first_and_batch_last(self):
        for collector, _, name, pipeline in self.pipelines():
            processors = pipeline.get('processors', [])
            self.assertGreaterEqual(len(processors), 2, f'{collector} {name}')
            self.assertEqual(processors[0], 'memory_limiter', f'{collector} {name}')
            self.assertEqual(processors[-1], 'batch', f'{collector} {name}')

    def test_push_receivers_have_their_own_pipelines(self):
        for collector, _, name, pipeline in self.push_pipelines():
            self.assertLessEqual(set(pipeline['receivers']), PUSH_RECEIVERS, f'{collector} {name}')

    def test_client_tenancy_is_removed_before_kubernetes_attributes(self):
        stripped = set(TENANT_LABELS.values()) | set(BUILD_LABELS.values()) | set(GATEWAY_LABELS.values()) | set(CLIENT_POD_IDENTITY)
        for collector, config, name, pipeline in self.push_pipelines():
            processors = pipeline['processors']
            enrichers = self.k8s_attributes(pipeline)
            self.assertEqual(len(enrichers), 1, f'{collector} {name}')
            strips = [index for index, processor in enumerate(processors) if processor.split('/')[0] == 'resource' and
                      stripped <= {action['key'] for action in config['processors'][processor]['attributes'] if action['action'] == 'delete'}]
            self.assertTrue(strips, f'{collector} {name} does not delete client tenancy attributes')
            self.assertLess(strips[0], processors.index(enrichers[0]), f'{collector} {name}')

    def test_push_pipelines_associate_pods_by_connection_only(self):
        for collector, config, name, pipeline in self.push_pipelines():
            for enricher in self.k8s_attributes(pipeline):
                self.assertEqual(config['processors'][enricher]['pod_association'], [{'sources': [{'from': 'connection'}]}], f'{collector} {name}')

    def test_file_logs_associate_pods_by_uid(self):
        config = self.configs['otel-col-daemon-agent']
        pipelines = [pipeline for pipeline in config['service']['pipelines'].values() if 'file_log' in pipeline['receivers']]
        self.assertEqual(len(pipelines), 1)
        enrichers = self.k8s_attributes(pipelines[0])
        self.assertEqual(len(enrichers), 1)
        self.assertEqual(config['processors'][enrichers[0]]['pod_association'], [{'sources': [{'from': 'resource_attribute', 'name': 'k8s.pod.uid'}]}])

    def test_collectors_can_read_the_pod_metadata_they_extract(self):
        needed = {('', 'pods'), ('', 'namespaces'), ('apps', 'replicasets')}
        roles = {document['metadata']['name']: document for document in self.documents if document['kind'] == 'ClusterRole'}
        for collector in ('otel-col-daemon', 'otel-col-deploy'):
            granted = {(group, resource) for rule in roles[collector]['rules'] if {'get', 'list', 'watch'} <= set(rule['verbs'])
                       for group in rule['apiGroups'] for resource in rule['resources']}
            self.assertLessEqual(needed, granted, collector)

    def test_build_logs_keep_their_build_id(self):
        config = self.configs['otel-col-daemon-agent']
        pipeline = next(pipeline for pipeline in config['service']['pipelines'].values() if 'file_log' in pipeline['receivers'])
        extract = config['processors'][self.k8s_attributes(pipeline)[0]]['extract']
        self.assertIn({'from': 'pod', 'key': 'loco.io/build-id', 'tag_name': 'loco.io/build-id'}, extract['labels'])

    def test_tenancy_comes_from_explicit_pod_labels(self):
        for collector, config, name, pipeline in self.pipelines():
            for enricher in self.k8s_attributes(pipeline):
                extract = config['processors'][enricher]['extract']
                labels = {rule.get('key'): rule.get('tag_name') for rule in extract['labels'] if rule['from'] == 'pod'}
                self.assertEqual(labels, TENANT_LABELS | BUILD_LABELS | GATEWAY_LABELS, f'{collector} {enricher}')
                self.assertTrue(all('key_regex' not in rule for rule in extract['labels']), f'{collector} {enricher}')
                self.assertNotIn('annotations', extract, f'{collector} {enricher}')
                self.assertFalse(extract.get('otel_annotations', False), f'{collector} {enricher}')
                for key in ('k8s.namespace.name', 'k8s.pod.name', 'k8s.pod.uid', 'k8s.deployment.name', 'service.name'):
                    self.assertIn(key, extract['metadata'], f'{collector} {enricher}')


class ClusterCollector(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.documents = render_chart('loco-obs', {})
        cls.configs = collector_relays(cls.documents)

    def test_only_the_singleton_collects_cluster_metrics_and_events(self):
        for receiver in ('k8s_cluster', 'k8sobjects'):
            collectors = {name for name, config in self.configs.items()
                          if receiver in config['receivers']}
            self.assertEqual(collectors, {'otel-col-cluster'}, receiver)
        deployment = next(document for document in self.documents
                          if document['kind'] == 'Deployment'
                          and document['metadata']['name'] == 'otel-col-cluster')
        self.assertEqual(deployment['spec']['replicas'], 1)
        self.assertEqual(deployment['spec']['strategy'], {'type': 'Recreate'})
        self.assertFalse(any(document['kind'] == 'HorizontalPodAutoscaler'
                             and document['spec']['scaleTargetRef']['name'] == 'otel-col-cluster'
                             for document in self.documents))

    def test_the_singleton_accepts_no_pushed_telemetry(self):
        config = self.configs['otel-col-cluster']
        self.assertFalse(PUSH_RECEIVERS & set(config['receivers']))
        for pipeline in config['service']['pipelines'].values():
            self.assertFalse(PUSH_RECEIVERS & set(pipeline['receivers']))
        services = [document for document in self.documents
                    if document['kind'] == 'Service'
                    and document['metadata']['name'] == 'otel-col-cluster']
        self.assertFalse({4317, 4318, 6831, 14250, 14268, 9411}
                         & {port['port'] for service in services for port in service['spec'].get('ports', [])})

    def test_ingest_keeps_only_push_pipelines(self):
        config = self.configs['otel-col-deploy']
        for pipeline in config['service']['pipelines'].values():
            self.assertLessEqual(set(pipeline['receivers']), PUSH_RECEIVERS)


class IngestAutoscaling(unittest.TestCase):
    def test_ingest_has_cpu_requests_and_an_hpa(self):
        documents = render_chart('loco-obs', {})
        values = chart_values('loco-obs')['otel-col-deploy']
        hpa = next(document for document in documents
                   if document['kind'] == 'HorizontalPodAutoscaler'
                   and document['spec']['scaleTargetRef']['name'] == 'otel-col-deploy')
        self.assertEqual(hpa['spec']['minReplicas'], values['autoscaling']['minReplicas'])
        self.assertEqual(hpa['spec']['maxReplicas'], values['autoscaling']['maxReplicas'])
        self.assertEqual(hpa['spec']['metrics'], [{
            'type': 'Resource', 'resource': {
                'name': 'cpu', 'target': {'type': 'Utilization',
                    'averageUtilization': values['autoscaling']['targetCPUUtilizationPercentage']},
            },
        }])
        deployment = next(document for document in documents
                          if document['kind'] == 'Deployment'
                          and document['metadata']['name'] == 'otel-col-deploy')
        self.assertNotIn('replicas', deployment['spec'])
        resources = deployment['spec']['template']['spec']['containers'][0]['resources']
        for resource in ('cpu', 'memory'):
            self.assertGreater(quantity(resources['requests'][resource]), 0)
            self.assertLessEqual(quantity(resources['requests'][resource]),
                                 quantity(resources['limits'][resource]))

    def test_disabling_autoscaling_restores_the_explicit_replica_count(self):
        documents = render_chart('loco-obs', {
            'otel-col-deploy': {'autoscaling': {'enabled': False}, 'replicaCount': 2}})
        self.assertFalse(any(document['kind'] == 'HorizontalPodAutoscaler' for document in documents))
        deployment = next(document for document in documents
                          if document['kind'] == 'Deployment'
                          and document['metadata']['name'] == 'otel-col-deploy')
        self.assertEqual(deployment['spec']['replicas'], 2)


class EnvoyTelemetry(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        documents = render_documents('loco-core', 'gateway.yaml', {})
        cls.proxy = next(document for document in documents if document['kind'] == 'EnvoyProxy')
        cls.core = chart_values('loco-core')
        cls.collector = collector_relays(render_chart('loco-obs', {}))['otel-col-deploy']

    def collector_address(self):
        observability = self.core['global']['observability']
        return f"{observability['otelCollectorDeployment']}.{observability['namespace']}.svc.cluster.local", observability['otelCollectorGrpcPort']

    def test_envoy_sends_every_signal_to_the_deployment_collector(self):
        obs = chart_values('loco-obs')
        host, port = self.collector_address()
        self.assertEqual(host.split('.')[0], obs['otel-col-deploy']['fullnameOverride'])
        self.assertEqual(self.collector['receivers']['otlp']['protocols']['grpc']['endpoint'], f'0.0.0.0:{port}')
        telemetry = self.proxy['spec']['telemetry']
        sinks = [sink['openTelemetry'] for sink in telemetry['metrics']['sinks']]
        sinks += [sink['openTelemetry'] for setting in telemetry['accessLog']['settings'] for sink in setting['sinks']]
        sinks.append(telemetry['tracing']['provider'])
        for sink in sinks:
            self.assertEqual((sink['host'], sink['port']), (host, port))
        self.assertEqual(telemetry['tracing']['provider']['type'], 'OpenTelemetry')
        self.assertTrue(all(sink['type'] == 'OpenTelemetry' for setting in telemetry['accessLog']['settings'] for sink in setting['sinks']))

    def test_tracing_samples_at_the_configured_rate(self):
        tracing = self.proxy['spec']['telemetry']['tracing']
        self.assertEqual(tracing['samplingRate'], self.core['envoyProxy']['tracing']['samplingRate'])

    def test_access_logs_carry_the_request_and_its_upstream(self):
        settings = self.proxy['spec']['telemetry']['accessLog']['settings']
        self.assertEqual(len(settings), 1)
        log_format = settings[0]['format']
        self.assertEqual(log_format['type'], 'JSON')
        self.assertEqual(log_format['json'], self.core['envoyProxy']['accessLog']['attributes'])
        operators = set(log_format['json'].values())
        for operator in ('%UPSTREAM_CLUSTER%', '%RESPONSE_CODE%', '%DURATION%', '%REQ(:METHOD)%', '%TRACE_ID%'):
            self.assertIn(operator, operators)
        self.assertTrue(any('PATH' in operator for operator in operators))
        self.assertEqual(log_format['json']['upstream_cluster'], '%UPSTREAM_CLUSTER%')

    def test_route_tenancy_runs_after_kubernetes_attributes(self):
        for name, pipeline in self.collector['service']['pipelines'].items():
            if 'otlp' not in pipeline['receivers']:
                continue
            processors = pipeline['processors']
            order = [processors.index(step) for step in ('k8s_attributes/connection', 'transform/envoy_tenancy', 'groupbyattrs/tenancy', 'batch')]
            self.assertEqual(order, sorted(order), name)
        self.assertEqual(self.collector['processors']['groupbyattrs/tenancy']['keys'], list(ROUTE_TENANCY))

    def test_only_envoy_telemetry_gets_route_tenancy(self):
        strip = {action['key'] for action in self.collector['processors']['resource/strip_tenancy']['attributes'] if action['action'] == 'delete'}
        transform = self.collector['processors']['transform/envoy_tenancy']
        for statements, attribute in ENVOY_CLUSTER_ATTRIBUTES.items():
            groups = transform[statements]
            unconditional = [group for group in groups if not group.get('conditions')]
            self.assertEqual(len(unconditional), 1, statements)
            for key in TENANT_LABELS.values():
                self.assertIn(f'delete_key(attributes, "{key}")', unconditional[0]['statements'], statements)
            gated = [group for group in groups if group.get('conditions')]
            self.assertEqual(len(gated), 1, statements)
            for condition in gated[0]['conditions']:
                self.assertIn('resource.attributes["gateway.envoyproxy.io/owning-gateway-name"] != nil', condition, statements)
            self.assertIn('gateway.envoyproxy.io/owning-gateway-name', strip)
            self.assertEqual(gated[0]['statements'][0], f'set(cache["cluster"], attributes["{attribute}"])', statements)
            for key in ROUTE_TENANCY:
                setters = [statement for statement in gated[0]['statements'] if statement.startswith(f'set(attributes["{key}"], cache[')]
                self.assertEqual(len(setters), 1, f'{statements} {key}')


class ControllerTelemetry(unittest.TestCase):
    def test_controller_reads_the_collector_from_values(self):
        deployment = render('loco-operator', 'controller/deployment.yaml', {
            'controller': {'image': {'tag': 'test'}},
            'observability': {'namespace': 'telemetry', 'collector': {'service': 'collector', 'grpcPort': 14317, 'httpPort': 14318}},
        })
        env = {item['name']: item for item in deployment['spec']['template']['spec']['containers'][0]['env']}
        self.assertEqual(env['LOCO_OBSERVABILITY_NAMESPACE']['value'], 'telemetry')
        self.assertEqual(env['LOCO_OTEL_COLLECTOR_SERVICE']['value'], 'collector')
        self.assertEqual(env['LOCO_OTEL_COLLECTOR_GRPC_PORT']['value'], '14317')
        self.assertEqual(env['LOCO_OTEL_COLLECTOR_HTTP_PORT']['value'], '14318')

    def test_operator_collector_matches_the_obs_chart(self):
        collector = chart_values('loco-operator')['observability']['collector']
        deploy = chart_values('loco-obs')['otel-col-deploy']
        self.assertEqual(collector['service'], deploy['fullnameOverride'])
        protocols = deploy['config']['receivers']['otlp']['protocols']
        self.assertEqual(protocols['grpc']['endpoint'].rsplit(':', 1)[1], str(collector['grpcPort']))
        self.assertEqual(protocols['http']['endpoint'].rsplit(':', 1)[1], str(collector['httpPort']))


class ObservabilityMigrationLock(unittest.TestCase):
    def deployment(self):
        return render('loco-control-plane', 'obs-proxy-deployment.yaml', {'obsProxy': TEST_PROXY})

    def test_proxy_has_no_kubernetes_api_credentials(self):
        pod = self.deployment()['spec']['template']['spec']
        self.assertNotIn('serviceAccountName', pod)
        self.assertIs(pod['automountServiceAccountToken'], False)

    def test_in_cluster_clickhouse_runs_a_single_node_keeper_for_the_lock(self):
        installation = clickhouse_installation({})
        config = installation['spec']['configuration']['files']['config.d/extra_config.xml']
        self.assertIn('<keeper_server>', config)
        self.assertIn('<keeper_map_path_prefix>', config)
        self.assertEqual(installation['spec']['configuration']['clusters'][0]['layout']['replicasCount'], 1)


class BuildNamespaceOwnership(unittest.TestCase):
    def test_chart_creates_build_namespace_by_default(self):
        namespace = render('loco-operator', 'builds/namespace.yaml', {})
        self.assertEqual(namespace['kind'], 'Namespace')
        self.assertEqual(namespace['metadata']['name'], 'loco-builds')
        self.assertEqual(namespace['metadata']['labels']['pod-security.kubernetes.io/enforce'], 'privileged')

    def test_gitops_can_own_build_namespace(self):
        namespace = render('loco-operator', 'builds/namespace.yaml', {'builds': {'createNamespace': False}})
        self.assertIsNone(namespace)


if __name__ == '__main__':
    unittest.main()
