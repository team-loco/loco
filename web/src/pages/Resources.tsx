import { useQuery } from "@connectrpc/connect-query";
import { parseAsString, useQueryStates } from "nuqs";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { ErrorState } from "@/components/design/ErrorState";
import { LimitCount, LimitedAction } from "@/components/design/LimitedAction";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { useDrafts } from "@/hooks/useDrafts";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { useOrgLimits } from "@/hooks/useOrgLimits";
import { useWorkspaceDeployments } from "@/hooks/useWorkspaceDeployments";
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
import { ResourcesEmpty } from "@/pages/resources/ResourcesEmpty";
import { ResourcesMap } from "@/pages/resources/ResourcesMap";
import { ResourceTable } from "@/pages/resources/ResourceTable";
import { buildRows, draftRows } from "@/pages/resources/table";

const NOW_TICK_MS = 60_000;
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
	const data = useWorkspaceDeployments(activeOrgId, activeWorkspaceId);
	const nowMs = useNow(NOW_TICK_MS).getTime();
	const [params, setParams] = useQueryStates(RESOURCES_PARAMS);
	const orgId = activeOrgId ?? "";
	const workspaceId = activeWorkspaceId ?? "";
	const { drafts } = useDrafts(orgId, workspaceId, active?.id);
	const configQuery = useQuery(getConfig, {}, { enabled: drafts.length > 0 });

	const loading = envsLoading || data.isLoading;
	const limits = useOrgLimits(orgId);
	const usage =
		data.isLoading || data.error !== null
			? null
			: limitUsage(data.resources.length, limits?.resourcesPerWorkspace, "resources", "workspace");
	const createBlocked = limitReason(usage);
	const total = data.resources.length + drafts.length;
	const { view, [DRAFT_PARAM]: draftId } = params;
	const setView = (next: ResourceView) => {
		void setParams({ view: next });
	};
	const serviceHref = newResourcePath(orgId, workspaceId, active === undefined ? {} : { env: active.name });

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
		if (total === 0) return <ResourcesEmpty environmentId={active?.id} newHref={serviceHref} />;
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
		const resources = buildRows(data.resources, data.deployments, environments, active?.id);
		return (
			<ResourceTable
				rows={
					active === undefined
						? resources
						: [...resources, ...draftRows(drafts, active.id, configQuery.data?.serviceDefaults)]
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
						{!loading && data.error === null && <LimitCount count={total} usage={usage} />}
					</>
				}
				actions={
					<div className="flex items-center gap-2.5">
						{!loading && total > 0 && <ViewToggle view={view} onView={setView} />}
						<LimitedAction usage={usage} reason={createBlocked}>
							<NewResourceMenu serviceHref={serviceHref} disabled={createBlocked !== null} />
						</LimitedAction>
					</div>
				}
			/>
			{body}
			{params[NEW_RESOURCE_PARAM] !== null && !envsLoading && (
				<NewService
					key={active?.id}
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
