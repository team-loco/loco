import { useMemo } from 'react'
import { useQuery } from '@connectrpc/connect-query'
import { AppCard } from '@/components/AppCard'
import { StatusBadge } from '@/components/StatusBadge'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { getCurrentUserOrgs } from '@/gen/org/v1/org-OrgService_connectquery'
import { listWorkspaces } from '@/gen/workspace/v1/workspace-WorkspaceService_connectquery'
import { listApps } from '@/gen/app/v1/app-AppService_connectquery'
import { listDeployments } from '@/gen/deployment/v1/deployment-DeploymentService_connectquery'

export function Home() {
  const { data: getCurrentUserOrgsRes, isLoading: orgsLoading, error: orgsError } = useQuery(
    getCurrentUserOrgs,
    {}
  )
  const orgs = getCurrentUserOrgsRes?.orgs ?? []
  const currentOrgId = orgs.length > 0 ? orgs[0].id : null

  const { data: listWorkspacesRes, isLoading: workspacesLoading } = useQuery(
    listWorkspaces,
    currentOrgId ? { orgId: currentOrgId } : undefined,
    { enabled: !!currentOrgId }
  )
  const workspaces = listWorkspacesRes?.workspaces ?? []
  const currentWorkspaceId = workspaces.length > 0 ? workspaces[0].id : null

  const { data: listAppsRes, isLoading: appsLoading, error: appsError } = useQuery(
    listApps,
    currentWorkspaceId ? { workspaceId: currentWorkspaceId } : undefined,
    { enabled: !!currentWorkspaceId }
  )
  const apps = listAppsRes?.apps ?? []

  const firstAppId = apps.length > 0 ? apps[0].id : null

  const { data: deploymentsRes } = useQuery(
    listDeployments,
    firstAppId ? { appId: firstAppId, limit: 5 } : undefined,
    { enabled: !!firstAppId }
  )

  const allDeployments = useMemo(() => {
    return (deploymentsRes?.deployments ?? [])
      .sort((a, b) => {
        const aTime = a.createdAt && typeof a.createdAt === 'object' && 'seconds' in a.createdAt
          ? Number(a.createdAt.seconds) * 1000
          : 0
        const bTime = b.createdAt && typeof b.createdAt === 'object' && 'seconds' in b.createdAt
          ? Number(b.createdAt.seconds) * 1000
          : 0
        return bTime - aTime
      })
      .slice(0, 5)
  }, [deploymentsRes])

  const isLoading = orgsLoading || workspacesLoading || appsLoading
  const error = orgsError || appsError

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-96">
        <div className="text-center">
          <div className="inline-flex gap-2 items-center">
            <div className="w-4 h-4 bg-main rounded-full animate-pulse"></div>
            <p className="text-foreground font-base">Loading...</p>
          </div>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex items-center justify-center min-h-96">
        <Card className="max-w-md">
          <CardContent className="p-6 text-center">
            <p className="text-destructive font-heading mb-4">Error Loading Data</p>
            <p className="text-sm text-foreground opacity-70 mb-4">{error.message}</p>
            <p className="text-xs text-foreground opacity-50">
              Make sure the backend is running on http://localhost:8000
            </p>
          </CardContent>
        </Card>
      </div>
    )
  }

  const currentOrg = orgs.length > 0 ? orgs[0] : null

  return (
    <div className="space-y-8">
      {/* Header */}
      <div className="space-y-2">
        <h2 className="text-3xl font-heading text-foreground">
          {currentOrg?.name || 'Dashboard'}
        </h2>
        <p className="text-foreground opacity-70">
          Manage your applications and deployments
        </p>
      </div>

      {/* Stats Row */}
      <div className="grid grid-cols-3 gap-4">
        <Card>
          <CardContent className="p-6">
            <p className="text-xs text-foreground opacity-60 font-base uppercase tracking-wide">
              Total Apps
            </p>
            <p className="text-3xl font-heading mt-2">{apps.length}</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-6">
            <p className="text-xs text-foreground opacity-60 font-base uppercase tracking-wide">
              Workspaces
            </p>
            <p className="text-3xl font-heading mt-2">{workspaces.length}</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-6">
            <p className="text-xs text-foreground opacity-60 font-base uppercase tracking-wide">
              Organizations
            </p>
            <p className="text-3xl font-heading mt-2">{orgs.length}</p>
          </CardContent>
        </Card>
      </div>

      {/* Main Apps Section */}
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-2xl font-heading">Your Applications</h3>
          <Button>+ New App</Button>
        </div>

        {apps.length > 0 ? (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {apps.map((app) => (
              <AppCard key={app.id} app={app} />
            ))}
          </div>
        ) : (
          <Card>
            <CardContent className="p-12 text-center">
              <p className="text-foreground opacity-60 font-base">No applications yet</p>
              <Button className="mt-4">Create your first app</Button>
            </CardContent>
          </Card>
        )}
      </div>

      {/* Recent Deployments */}
      <div className="space-y-4">
        <h3 className="text-2xl font-heading">Recent Deployments</h3>

        {allDeployments.length > 0 ? (
          <Card>
            <CardContent className="p-0">
              <div className="divide-y divide-border">
                {allDeployments.map((deployment) => (
                  <div
                    key={deployment.id}
                    className="px-6 py-4 flex items-center justify-between hover:bg-background transition-colors cursor-pointer"
                  >
                    <div className="flex-1">
                      <p className="font-base text-foreground">
                        {deployment.image}
                        {deployment.replicas ? ` (${deployment.replicas} replicas)` : ''}
                      </p>
                      <p className="text-xs text-foreground opacity-60 mt-1">
                        {deployment.createdAt && typeof deployment.createdAt === 'object' && 'seconds' in deployment.createdAt
                          ? new Date(Number(deployment.createdAt.seconds) * 1000).toLocaleString('en-US', {
                              month: 'short',
                              day: 'numeric',
                              hour: '2-digit',
                              minute: '2-digit',
                            })
                          : 'unknown'}
                      </p>
                    </div>
                    <StatusBadge status={deployment.status} />
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        ) : (
          <Card>
            <CardContent className="p-8 text-center">
              <p className="text-foreground opacity-60 font-base">No deployments yet</p>
            </CardContent>
          </Card>
        )}
      </div>
    </div>
  )
}
