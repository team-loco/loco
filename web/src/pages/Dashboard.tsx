import { useQueryState } from "nuqs";
import { useQuery } from "@connectrpc/connect-query";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { Link } from "react-router";

import { EmptyState } from "@/components/design/EmptyState";
import { NavArrow } from "@/components/design/NavArrow";
import { ErrorState } from "@/components/design/ErrorState";
import { Page, Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { ResourceAccess } from "@/components/ResourceAccess";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useDrafts } from "@/hooks/useDrafts";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { usePollingWorkspaceResources } from "@/hooks/useWorkspaceDeployments";
import { DRAFT_PARAM, newResourcePath, resourcePath, settingsPath } from "@/lib/routes";
import { EmptyWorkspace } from "@/pages/dashboard/EmptyWorkspace";
import { RecentDeployments } from "@/pages/dashboard/RecentDeployments";
import { draftItems } from "@/pages/dashboard/resourceRows";
import { ResourcesTable } from "@/pages/dashboard/ResourcesTable";
import { Totals } from "@/pages/dashboard/Totals";
import { useEnvironmentRegions } from "@/pages/dashboard/useDashboardData";
import { DraftSheet } from "@/pages/resources/NewService";

const NOW_TICK_MS = 60_000;
const SKELETON_ROWS = 4;
const TOTAL_POLL_MS = 15_000;

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
	const { grants, regions } = useEnvironmentRegions(activeOrgId, activeWorkspaceId, active?.id);
	const nowMs = useNow(NOW_TICK_MS).getTime();

	const orgId = activeOrgId ?? "";
	const workspaceId = activeWorkspaceId ?? "";
	const scoped = grants !== undefined && grants !== "workspace";
	const counted = useQuery(
		listWorkspaceResources,
		{ workspaceId, environmentId: active?.id ?? "", environmentScoped: true, pageSize: 1, includeTotalCount: true },
		{ enabled: grants === "workspace" && active !== undefined, refetchInterval: TOTAL_POLL_MS },
	);
	const granted = usePollingWorkspaceResources(activeOrgId, activeWorkspaceId, scoped);
	const data = scoped ? granted : counted;
	const envTotal = counted.data?.totalCount ?? 0;
	const { drafts } = useDrafts(orgId, workspaceId, active?.id);
	const [draftId] = useQueryState(DRAFT_PARAM);
	const defaultRegion = useQuery(getConfig, {}).data?.defaultRegion ?? "";
	const hrefFor = (id: string) => resourcePath(orgId, workspaceId, id, { env: active?.slug ?? null });
	const otherEnv = environments.find((e) => e.id !== active?.id);
	// Until grants resolve a scoped viewer is indistinguishable from a workspace reader, and the
	// workspace view queries environment-wide metrics the proxy refuses them.
	const loading = envsLoading || grants === undefined || data.isLoading;
	const heading = <h1 className="sr-only">Dashboard</h1>;

	if (data.error !== null) {
		return (
			<Page>
				{heading}
				<ErrorState error={data.error} what="the dashboard" rpc={listWorkspaceResources} onRetry={data.refetch} />
			</Page>
		);
	}

	if (scoped) {
		return (
			<Page>
				{heading}
				{granted.isLoading ? (
					<DashboardSkeleton />
				) : (
					<ResourceAccess orgId={orgId} workspaceId={workspaceId} resources={granted.data?.resources ?? []} />
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
						<Link to={settingsPath(orgId, workspaceId, "workspace")} className="group/nav text-foreground">
							workspace settings
							<NavArrow className="ml-1 inline-block" />
						</Link>{" "}
						to start deploying.
					</EmptyState>
				</Section>
			</Page>
		);
	}

	return (
		<Page className="gap-14 pt-9">
			{heading}
			{loading || active === undefined ? (
				<DashboardSkeleton />
			) : envTotal === 0 && drafts.length === 0 ? (
				<EmptyWorkspace
					envName={active.name}
					newHref={newResourcePath(orgId, workspaceId, { env: active.slug })}
					otherEnvName={otherEnv?.name ?? null}
					onSwitchEnv={() => {
						if (otherEnv !== undefined) setActive(otherEnv);
					}}
				/>
			) : (
				<>
					<Totals
						orgId={orgId}
						workspaceId={workspaceId}
						environmentId={active.id}
						envName={active.slug}
						hrefFor={hrefFor}
						nowMs={nowMs}
					/>
					<ResourcesTable
						key={active.id}
						total={envTotal}
						drafts={draftItems(drafts, regions, defaultRegion)}
						regions={regions}
						orgId={orgId}
						workspaceId={workspaceId}
						environmentId={active.id}
						envName={active.slug}
						nowMs={nowMs}
						hrefFor={hrefFor}
					/>
					<RecentDeployments
						orgId={orgId}
						workspaceId={workspaceId}
						environmentId={active.id}
						envSlug={active.slug}
						envName={active.name}
						nowMs={nowMs}
					/>
				</>
			)}
			{active !== undefined && <DraftSheet orgId={orgId} workspaceId={workspaceId} env={active} draftId={draftId} />}
		</Page>
	);
}
