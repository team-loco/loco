import { useQuery } from "@connectrpc/connect-query";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { parseAsString, useQueryStates } from "nuqs";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { listRegions, listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { ErrorState } from "@/components/design/ErrorState";
import { LimitCount, LimitedAction } from "@/components/design/LimitedAction";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { ResourceAccess } from "@/components/ResourceAccess";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { useDrafts } from "@/hooks/useDrafts";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useNow } from "@/hooks/useNow";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import { limitReason, limitUsage } from "@/lib/limits";
import {
	DRAFT_PARAM,
	NEW_RESOURCE_PARAM,
	newResourcePath,
	RESOURCE_VIEWS,
	resourceViewParser,
	type ResourceView,
} from "@/lib/routes";
import { NewResourceMenu } from "@/pages/resources/NewResourceMenu";
import { DraftSheet, NewService } from "@/pages/resources/NewService";
import { EmptyWorkspace } from "@/pages/dashboard/EmptyWorkspace";
import { ResourcesMap } from "@/pages/resources/ResourcesMap";
import { ResourceTable } from "@/pages/resources/ResourceTable";
import { draftRows } from "@/pages/resources/table";

const NOW_TICK_MS = 60_000;
const TOTAL_POLL_MS = 15_000;
const SKELETON_ROWS = 6;
const RESOURCES_PARAMS = {
	view: resourceViewParser,
	[DRAFT_PARAM]: parseAsString,
	[NEW_RESOURCE_PARAM]: parseAsString,
};

function ViewToggle({ view, onView }: { view: ResourceView; onView: (view: ResourceView) => void }) {
	return (
		<ToggleGroup
			variant="segmented"
			aria-label="View"
			value={[view]}
			onValueChange={(v: string[]) => {
				const next = RESOURCE_VIEWS.find((r) => r === v[0]);
				if (next !== undefined) onView(next);
			}}
		>
			<ToggleGroupItem value="table">Table</ToggleGroupItem>
			<ToggleGroupItem value="map">Map</ToggleGroupItem>
		</ToggleGroup>
	);
}

export function Resources() {
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { environments, active, isLoading: envsLoading } = useEnvironments();
	const nowMs = useNow(NOW_TICK_MS).getTime();
	const [params, setParams] = useQueryStates(RESOURCES_PARAMS);
	const orgId = activeOrgId ?? "";
	const workspaceId = activeWorkspaceId ?? "";
	const grants = useMyScopes().resourceGrants(orgId, workspaceId);
	const scoped = grants !== undefined && grants !== "workspace";
	const counted = useQuery(
		listWorkspaceResources,
		{ workspaceId, pageSize: 1, includeTotalCount: true },
		{ enabled: grants === "workspace" && workspaceId !== "", refetchInterval: TOTAL_POLL_MS },
	);
	const granted = useTanstackQuery({ ...useWorkspaceResourcesOptions(orgId, workspaceId), enabled: scoped });
	const { drafts } = useDrafts(orgId, workspaceId, active?.id);
	const configQuery = useQuery(getConfig, {}, { enabled: drafts.length > 0 });
	const regionsQuery = useQuery(
		listRegions,
		{ environmentId: active?.id ?? "" },
		{ enabled: drafts.length > 0 && active !== undefined },
	);

	const data = scoped ? granted : counted;
	const loading = envsLoading || grants === undefined || data.isPending;
	const limits = useOrgLimits(orgId);
	const usage =
		loading || scoped || counted.error !== null
			? null
			: limitUsage(counted.data?.totalCount ?? 0, limits?.resourcesPerWorkspace, "resources", "workspace");
	const createBlocked = limitReason(usage);
	const total = (counted.data?.totalCount ?? 0) + drafts.length;
	const { view, [DRAFT_PARAM]: draftId } = params;
	const setView = (next: ResourceView) => {
		void setParams({ view: next }, { history: "replace" });
	};
	const serviceHref = newResourcePath(orgId, workspaceId, active === undefined ? {} : { env: active.slug });

	const body = (() => {
		if (data.error !== null) {
			return <ErrorState error={data.error} what="resources" rpc={listWorkspaceResources} onRetry={data.refetch} />;
		}
		if (loading) {
			return (
				<Section>
					<div className="flex flex-col gap-2 p-5">
						{Array.from({ length: SKELETON_ROWS }, (_, i) => (
							<Skeleton key={i} className="h-11 w-full" />
						))}
					</div>
				</Section>
			);
		}
		if (scoped)
			return <ResourceAccess orgId={orgId} workspaceId={workspaceId} resources={granted.data?.resources ?? []} />;
		if (total === 0) return <EmptyWorkspace wholeWorkspace />;
		if (view === "map" && active !== undefined) {
			return (
				<ResourcesMap
					orgId={orgId}
					workspaceId={workspaceId}
					env={active}
					onShowTable={() => {
						setView("table");
					}}
				/>
			);
		}
		return (
			<ResourceTable
				drafts={
					active === undefined
						? []
						: draftRows(
								drafts,
								active.id,
								configQuery.data?.serviceDefaults,
								regionsQuery.data?.regions ?? [],
								configQuery.data?.defaultRegion ?? "",
							)
				}
				environments={environments}
				active={active}
				orgId={orgId}
				workspaceId={workspaceId}
				nowMs={nowMs}
			/>
		);
	})();

	return (
		<Page>
			<PageHeader
				title={
					<>
						Resources
						{!loading && !scoped && data.error === null && <LimitCount usage={usage} />}
					</>
				}
				actions={
					<div className="flex items-center gap-2.5">
						{!loading && !scoped && total > 0 && <ViewToggle view={view} onView={setView} />}
						<LimitedAction reason={createBlocked}>
							<NewResourceMenu serviceHref={serviceHref} disabled={createBlocked !== null} />
						</LimitedAction>
					</div>
				}
			/>
			{body}
			{params[NEW_RESOURCE_PARAM] !== null && (
				<NewService
					loading={envsLoading}
					orgId={orgId}
					workspaceId={workspaceId}
					env={active}
					onClose={() => {
						void setParams({ [NEW_RESOURCE_PARAM]: null });
					}}
					onCreated={(id) => {
						void setParams({ [NEW_RESOURCE_PARAM]: null, [DRAFT_PARAM]: id });
					}}
				/>
			)}
			{active !== undefined && <DraftSheet orgId={orgId} workspaceId={workspaceId} env={active} draftId={draftId} />}
		</Page>
	);
}
