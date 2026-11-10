import { Link } from "react-router";

import { EmptyState } from "@/components/design/EmptyState";
import { Page, Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { getErrorMessage } from "@/lib/error-handler";
import { newResourcePath, resourcePath, settingsPath } from "@/lib/routes";
import { allocation } from "@/pages/dashboard/allocation";
import { EmptyWorkspace } from "@/pages/dashboard/EmptyWorkspace";
import { RecentDeployments } from "@/pages/dashboard/RecentDeployments";
import { ResourcesTable } from "@/pages/dashboard/ResourcesTable";
import { Totals } from "@/pages/dashboard/Totals";
import { useDashboardData } from "@/pages/dashboard/useDashboardData";

const NOW_TICK_MS = 60_000;
const SKELETON_ROWS = 4;

function DashboardSkeleton() {
	return (
		<Section title="Resources">
			<div className="flex flex-col gap-2 p-4">
				{Array.from({ length: SKELETON_ROWS }, (_, i) => (
					<Skeleton key={i} className="h-9 w-full" />
				))}
			</div>
		</Section>
	);
}

export function Dashboard() {
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { environments, active, setActive, isLoading: envsLoading } = useEnvironments();
	const data = useDashboardData(activeWorkspaceId, active);
	const nowMs = useNow(NOW_TICK_MS).getTime();

	const orgId = activeOrgId ?? "";
	const workspaceId = activeWorkspaceId ?? "";
	const hrefFor = (id: string) => resourcePath(orgId, workspaceId, id);
	const otherEnv = environments.find((e) => e.id !== active?.id);
	const loading = envsLoading || data.isLoading;
	const heading = <h1 className="sr-only">Dashboard</h1>;

	if (data.error !== null) {
		return (
			<Page>
				{heading}
				<Section>
					<EmptyState title="Couldn't load resources">
						{getErrorMessage(data.error, "Failed to load resources")}
					</EmptyState>
				</Section>
			</Page>
		);
	}

	if (!envsLoading && active === undefined) {
		return (
			<Page>
				{heading}
				<Section>
					<EmptyState title="No environments yet">
						Create one in{" "}
						<Link to={settingsPath(orgId, workspaceId, "workspace")} className="text-foreground underline">
							workspace settings
						</Link>{" "}
						to start deploying.
					</EmptyState>
				</Section>
			</Page>
		);
	}

	return (
		<Page>
			{heading}
			{loading || active === undefined ? (
				<DashboardSkeleton />
			) : data.envResources.length === 0 ? (
				<EmptyWorkspace
					envName={active.name}
					newHref={newResourcePath(orgId, workspaceId, { env: active.name })}
					otherEnvName={otherEnv?.name ?? null}
					onSwitchEnv={() => {
						if (otherEnv !== undefined) setActive(otherEnv);
					}}
				/>
			) : (
				<>
					<Totals value={allocation(data.envResources)} hrefFor={hrefFor} />
					<ResourcesTable
						key={active.id}
						items={data.envResources}
						regions={data.regions}
						orgId={orgId}
						workspaceId={workspaceId}
						environmentId={active.id}
						envName={active.name}
						nowMs={nowMs}
						hrefFor={hrefFor}
					/>
					<RecentDeployments
						deployments={data.envDeployments}
						orgId={orgId}
						workspaceId={workspaceId}
						envName={active.name}
						nowMs={nowMs}
					/>
				</>
			)}
		</Page>
	);
}
