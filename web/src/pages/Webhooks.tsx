import { Navigate, useParams } from "react-router";
import { useQuery } from "@connectrpc/connect-query";
import { Scope } from "@gen/loco/token/v1/token_pb";
import { getWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { Page } from "@/components/design/Page";
import { ErrorState } from "@/components/design/ErrorState";
import { Skeleton } from "@/components/design/Skeleton";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { WebhooksPage } from "./webhooks/WebhooksPage";

export function Webhooks() {
	const { orgId = "", workspaceId = "" } = useParams();
	const scopes = useMyScopes();
	const admin = scopes.workspaceLevel(orgId, workspaceId) >= scopeLevel(Scope.ADMIN);
	const query = useQuery(getWorkspace, { workspaceId }, { enabled: admin });
	if (scopes.isLoading || (admin && query.isPending))
		return (
			<Page>
				<Skeleton className="h-40" />
			</Page>
		);
	if (!admin) return <Navigate to=".." relative="path" replace />;
	if (query.data?.workspace === undefined)
		return (
			<Page>
				<ErrorState error={query.error} what="webhooks" rpc={getWorkspace} scope="role" onRetry={query.refetch} />
			</Page>
		);
	return (
		<WebhooksPage key={workspaceId} orgId={orgId} workspaceId={workspaceId} workspaceName={query.data.workspace.name} />
	);
}
