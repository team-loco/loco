
def prepare_activity(evidence, state):
    assert state['container'].startswith(evidence.LABEL + '-')
    if 'activity' in state:
        return state['activity']
    api = state['api_url']
    owner = state['users']['owner']['token']

    def call(method, body):
        return evidence.rpc(api, owner, method, body)

    org = call('loco.org.v1.OrgService/ListUserOrgs', {'userId': state['users']['owner']['id']})['orgs'][0]['id']
    workspace = state['workspaces']['acme/shared']
    sibling = state['workspaces']['acme/sibling']
    call('loco.environment.v1.EnvironmentService/CreateEnvironment', {'workspaceId': workspace, 'name': 'staging', 'type': 'ENVIRONMENT_TYPE_STAGING'})
    spec = {'service': {'routing': {'port': 8080}, 'regions': {'us-east-1': {'enabled': True, 'cpu': '100m', 'memory': '128Mi'}}}}
    resource = call('loco.resource.v1.ResourceService/CreateResource', {'workspaceId': workspace, 'name': 'checkout', 'type': 'RESOURCE_TYPE_SERVICE', 'spec': spec})['resourceId']
    sibling_resource = call('loco.resource.v1.ResourceService/CreateResource', {'workspaceId': sibling, 'name': 'inventory', 'type': 'RESOURCE_TYPE_SERVICE', 'spec': spec})['resourceId']
    other = call('loco.org.v1.OrgService/CreateOrg', {'name': 'other-company'})['orgId']
    other_workspace = call('loco.workspace.v1.WorkspaceService/CreateWorkspace', {'orgId': other, 'name': 'internal'})['workspaceId']
    for name in ('workspace_admin', 'resource_reader'):
        call('loco.org.v1.OrgService/AddOrgMember', {'orgId': other, 'userId': state['users'][name]['id']})
    scopes = [(state['users']['workspace_admin']['id'], scope, 'workspace', workspace) for scope in ('read', 'write', 'admin')]
    scopes.append((state['users']['resource_reader']['id'], 'read', 'resource', resource))
    values = ','.join('(' + ','.join(evidence.sql_literal(value) for value in row) + ')' for row in scopes)
    evidence.psql(state['container'], 'INSERT INTO user_scopes(user_id,scope,entity_type,entity_id) VALUES ' + values + ';')
    for index in range(53):
        call('loco.resource.v1.ResourceService/UpdateResource', {'resourceId': resource, 'description': f'Checkout activity revision {index}'})
    evidence.rpc(api, state['users']['workspace_admin']['token'], 'loco.workspace.v1.WorkspaceService/UpdateWorkspace', {'workspaceId': workspace, 'description': 'Commerce services managed by the checkout team'})
    return {'orgId': org, 'workspaceId': workspace, 'siblingId': sibling, 'resourceId': resource, 'siblingResourceId': sibling_resource, 'otherOrgId': other, 'otherWorkspaceId': other_workspace}


def verify_activity_access(evidence, state, activity):
    api = state['api_url']
    method = 'loco.event.v1.EventService/ListEvents'
    workspace_scope = {'orgId': activity['orgId'], 'workspaceId': activity['workspaceId']}
    resource_scope = {**workspace_scope, 'resourceId': activity['resourceId']}
    for user, scope in [('workspace_admin', workspace_scope), ('resource_reader', resource_scope)]:
        token = state['users'][user]['token']
        granted = evidence.rpc(api, token, 'loco.token.v1.TokenService/GetScopes', {})['scopes']
        assert not any(item.get('entityType') == 'ENTITY_TYPE_ORGANIZATION' and item.get('entityId') == activity['orgId'] for item in granted)
        first = evidence.rpc(api, token, method, {**scope, 'pageSize': 50})
        assert len(first['events']) == 50 and first.get('nextBeforeSeq')
        assert all(event['orgId'] == activity['orgId'] and event['workspaceId'] == activity['workspaceId'] for event in first['events'])
        if user == 'resource_reader':
            assert all(event['resourceId'] == activity['resourceId'] for event in first['events'])
        second = evidence.rpc(api, token, method, {**scope, 'pageSize': 50, 'beforeSeq': first['nextBeforeSeq']})
        assert second['events']
        assert not {event['id'] for event in first['events']} & {event['id'] for event in second['events']}
        filtered = evidence.rpc(api, token, method, {**scope, 'types': ['resource.created']})
        assert len(filtered['events']) == 1 and filtered['events'][0]['type'] == 'resource.created'
        denied = [{'orgId': activity['orgId']}, {'orgId': activity['orgId'], 'workspaceId': activity['siblingId']}, {'orgId': activity['otherOrgId'], 'workspaceId': activity['otherWorkspaceId']}]
        if user == 'resource_reader':
            denied.extend([workspace_scope, {**workspace_scope, 'resourceId': activity['siblingResourceId']}])
        for body in denied:
            try:
                evidence.rpc(api, token, method, body)
            except evidence.RPCError as error:
                assert error.code == 'permission_denied', (user, body, error.code)
            else:
                raise AssertionError((user, body, 'unexpected access'))
        try:
            evidence.rpc(api, token, method, {**scope, 'orgId': activity['otherOrgId']})
        except evidence.RPCError as error:
            assert error.code == 'not_found', (user, error.code)
        else:
            raise AssertionError((user, 'mismatched parent accepted'))
    return {'limitedScopeSuccess': True, 'widerAndOtherScopeDenied': True, 'pagingAndTypes': True, 'parentConsistency': True}
