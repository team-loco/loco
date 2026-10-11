import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
COLLECTOR_CONFIGMAPS = ('otel-col-daemon-agent', 'otel-col-deploy')
PUSH_RECEIVERS = {'otlp', 'jaeger', 'zipkin'}
TENANT_LABELS = {
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
        rendered = subprocess.check_output(['helm', 'template', chart, str(root), '-n', 'platform-test', '-f', str(root / 'overrides.json')], text=True)
        documents = subprocess.check_output(['yq', '-o=json', '-I=0', '.', '-'], input=rendered, text=True)
        return [json.loads(line) for line in documents.splitlines() if line.strip() not in ('', 'null')]


def render_chart(chart, overrides):
    with tempfile.TemporaryDirectory() as directory:
        values = Path(directory) / 'overrides.json'
        values.write_text(json.dumps(overrides))
        rendered = subprocess.check_output(['helm', 'template', chart, str(ROOT / 'charts' / chart), '-n', 'platform-test', '-f', str(values)], text=True)
    documents = subprocess.check_output(['yq', '-o=json', '-I=0', '.', '-'], input=rendered, text=True)
    return [json.loads(line) for line in documents.splitlines() if line.strip() not in ('', 'null')]


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
        deployment = render('loco-obs', 'obs-proxy-deployment.yaml', {
            'obsProxy': {'image': {'tag': 'test'}, 'auth': {'existingSecret': 'proxy-auth'},
                         'clickhouse': {'existingSecret': {'name': 'proxy-database', 'key': 'connection'}}},
        })
        env = {item['name']: item for item in deployment['spec']['template']['spec']['containers'][0]['env']}
        self.assertEqual(env['CLICKHOUSE_URL']['valueFrom']['secretKeyRef'], {'name': 'proxy-database', 'key': 'connection'})
        self.assertNotIn('value', env['CLICKHOUSE_URL'])
        self.assertEqual(env['PROXY_AUTH_TOKEN']['valueFrom']['secretKeyRef'], {'name': 'proxy-auth', 'key': 'token', 'optional': False})

    def test_local_defaults_remain_available(self):
        deployment = render('loco-obs', 'obs-proxy-deployment.yaml', {'obsProxy': {'image': {'tag': 'test'}}})
        env = {item['name']: item for item in deployment['spec']['template']['spec']['containers'][0]['env']}
        self.assertEqual(env['CLICKHOUSE_URL']['value'], 'clickhouse://loco_reader:$(CLICKHOUSE_READER_PASSWORD)@clickhouse-loco-obs-clickhouse.platform-test.svc.cluster.local:9000')
        self.assertTrue(env['PROXY_AUTH_TOKEN']['valueFrom']['secretKeyRef']['optional'])

    def test_missing_database_secret_key_is_rejected(self):
        with self.assertRaises(subprocess.CalledProcessError):
            render('loco-obs', 'obs-proxy-deployment.yaml', {'obsProxy': {'image': {'tag': 'test'}, 'clickhouse': {'existingSecret': {'name': 'db', 'key': ''}}}})


class ObservabilitySchema(unittest.TestCase):
    def proxy_env(self, overrides=None):
        values = {'obsProxy': {'image': {'tag': 'test'}}}
        for key, value in (overrides or {}).items():
            values['obsProxy'][key] = value
        deployment = render('loco-obs', 'obs-proxy-deployment.yaml', values)
        container = deployment['spec']['template']['spec']['containers'][0]
        return container, [item['name'] for item in container['env']], {item['name']: item for item in container['env']}

    def test_proxy_connects_as_the_reader_and_migrator_users(self):
        _, order, env = self.proxy_env()
        host = 'clickhouse-loco-obs-clickhouse.platform-test.svc.cluster.local:9000'
        self.assertEqual(env['CLICKHOUSE_MIGRATOR_URL']['value'], 'clickhouse://loco_migrator:$(CLICKHOUSE_MIGRATOR_PASSWORD)@' + host)
        self.assertEqual(env['CLICKHOUSE_READER_PASSWORD']['valueFrom']['secretKeyRef'], {'name': 'loco-obs-clickhouse-reader', 'key': 'password'})
        self.assertEqual(env['CLICKHOUSE_MIGRATOR_PASSWORD']['valueFrom']['secretKeyRef'], {'name': 'loco-obs-clickhouse-migrator', 'key': 'password'})
        self.assertLess(order.index('CLICKHOUSE_READER_PASSWORD'), order.index('CLICKHOUSE_URL'))
        self.assertLess(order.index('CLICKHOUSE_MIGRATOR_PASSWORD'), order.index('CLICKHOUSE_MIGRATOR_URL'))

    def test_migrator_dsn_can_come_from_an_existing_secret(self):
        _, _, env = self.proxy_env({'clickhouse': {'migratorExistingSecret': {'name': 'proxy-migrator', 'key': 'dsn'}}})
        self.assertEqual(env['CLICKHOUSE_MIGRATOR_URL']['valueFrom']['secretKeyRef'], {'name': 'proxy-migrator', 'key': 'dsn'})
        self.assertNotIn('CLICKHOUSE_MIGRATOR_PASSWORD', env)

    def test_proxy_owns_schema_settings(self):
        values = chart_values('loco-obs')['obsProxy']['clickhouse']
        container, _, env = self.proxy_env()
        self.assertEqual(env['CLICKHOUSE_DB']['value'], 'loco_obs')
        self.assertEqual(env['CLICKHOUSE_LOGS_TTL']['value'], values['retention']['logs'])
        self.assertEqual(env['CLICKHOUSE_TRACES_TTL']['value'], values['retention']['traces'])
        self.assertEqual(env['CLICKHOUSE_METRICS_TTL']['value'], values['retention']['metrics'])
        budget = values['migrations']['retryBudgetSeconds']
        self.assertEqual(env['MIGRATION_RETRY_BUDGET']['value'], f'{budget}s')
        self.assertEqual(env['MIGRATION_RETRY_INTERVAL']['value'], f"{values['migrations']['retryIntervalSeconds']}s")
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

    def test_collectors_ingest_into_the_proxy_database(self):
        values = chart_values('loco-obs')
        database = values['obsProxy']['clickhouse']['database']
        users = {user['name']: user for user in values['clickhouse']['clickhouse']['users']}
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
        cls.documents = render_chart('loco-obs', {'obsProxy': {'image': {'tag': 'test'}}})
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

    def test_both_collectors_render(self):
        self.assertEqual(set(self.configs), set(COLLECTOR_CONFIGMAPS))

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


class EnvoyTelemetry(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        documents = render_documents('loco-core', 'gateway.yaml', {})
        cls.proxy = next(document for document in documents if document['kind'] == 'EnvoyProxy')
        cls.core = chart_values('loco-core')
        cls.collector = collector_relays(render_chart('loco-obs', {'obsProxy': {'image': {'tag': 'test'}}}))['otel-col-deploy']

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
