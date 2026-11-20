import { Navigate, useParams } from "react-router";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { Page } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { useEnvironments } from "@/hooks/useEnvironment";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";

import { SecretsPage } from "./secrets/SecretsPage";

export function Secrets() {
	const { orgId = "", workspaceId = "" } = useParams();
	const scopes = useMyScopes();
	const { active, isLoading } = useEnvironments();
	const level = scopes.workspaceLevel(orgId, workspaceId);
	if (scopes.isLoading || isLoading)
		return (
			<Page>
				<Skeleton className="h-40" />
			</Page>
		);
	if (level < scopeLevel(Scope.READ) || active === undefined) return <Navigate to=".." relative="path" replace />;
	return (
		<SecretsPage
			key={active.id}
			orgId={orgId}
			workspaceId={workspaceId}
			environmentId={active.id}
			environmentName={active.name}
			environmentSlug={active.slug}
			canWrite={level >= scopeLevel(Scope.WRITE)}
		/>
	);
}
