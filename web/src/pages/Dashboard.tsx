import { Link, useSearchParams } from "react-router";

import { EmptyState } from "@/components/design/EmptyState";
import { Page, Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { ResourceAccess } from "@/components/ResourceAccess";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useDrafts } from "@/hooks/useDrafts";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { getErrorMessage } from "@/lib/error-handler";
import { DRAFT_PARAM, newResourcePath, resourcePath, settingsPath } from "@/lib/routes";
import { allocation } from "@/pages/dashboard/allocation";
import { EmptyWorkspace } from "@/pages/dashboard/EmptyWorkspace";
import { RecentDeployments } from "@/pages/dashboard/RecentDeployments";
import { draftItems } from "@/pages/dashboard/resourceRows";
import { ResourcesTable } from "@/pages/dashboard/ResourcesTable";
import { Totals } from "@/pages/dashboard/Totals";
import { useDashboardData } from "@/pages/dashboard/useDashboardData";
import { DraftSheet } from "@/pages/resources/NewService";

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
	const data = useDashboardData(activeOrgId, activeWorkspaceId, active);
	const nowMs = useNow(NOW_TICK_MS).getTime();
	const [params] = useSearchParams();

	const orgId = activeOrgId ?? "";
	const workspaceId = activeWorkspaceId ?? "";
	const { drafts } = useDrafts(orgId, workspaceId, active?.id);
	const draftId = params.get(DRAFT_PARAM);
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

	if (data.scoped) {
		return (
			<Page>
				{heading}
				{data.isLoading ? (
					<DashboardSkeleton />
				) : (
					<ResourceAccess orgId={orgId} workspaceId={workspaceId} resources={data.resources} />
				)}
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
			) : data.envResources.length === 0 && drafts.length === 0 ? (
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
					<Totals
						workspaceId={workspaceId}
						environmentId={active.id}
						value={allocation(data.envResources)}
						hrefFor={hrefFor}
					/>
					<ResourcesTable
						key={active.id}
						items={[...data.envResources, ...draftItems(drafts, data.regions)]}
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
			{draftId !== null && active !== undefined && (
				<DraftSheet key={draftId} orgId={orgId} workspaceId={workspaceId} env={active} draftId={draftId} />
			)}
		</Page>
	);
}
