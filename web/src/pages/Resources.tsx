import { useQuery } from "@connectrpc/connect-query";
import { useSearchParams } from "react-router";
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
import { DRAFT_PARAM, draftSearch, NEW_RESOURCE_PARAM, newResourcePath } from "@/lib/routes";
import { NewResourceMenu } from "@/pages/resources/NewResourceMenu";
import { DraftSheet, NewService } from "@/pages/resources/NewService";
import { ResourcesEmpty } from "@/pages/resources/ResourcesEmpty";
import { ResourcesMap } from "@/pages/resources/ResourcesMap";
import { ResourceTable } from "@/pages/resources/ResourceTable";
import { buildRows, draftRows } from "@/pages/resources/table";

const NOW_TICK_MS = 60_000;
const SKELETON_ROWS = 6;
const TABLE_VIEW = "table";
const MAP_VIEW = "map";
const VIEW_PARAM = "view";

function ViewToggle({ view, onView }: { view: string; onView: (view: string) => void }) {
	return (
		<ToggleGroup
			variant="segmented"
			aria-label="View"
			value={[view]}
			onValueChange={(v: string[]) => {
				if (v[0] !== undefined) onView(v[0]);
			}}
		>
			<ToggleGroupItem value={TABLE_VIEW}>Table</ToggleGroupItem>
			<ToggleGroupItem value={MAP_VIEW}>Map</ToggleGroupItem>
		</ToggleGroup>
	);
}

export function Resources() {
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { environments, active, isLoading: envsLoading } = useEnvironments();
	const data = useWorkspaceDeployments(activeOrgId, activeWorkspaceId);
	const nowMs = useNow(NOW_TICK_MS).getTime();
	const [params, setParams] = useSearchParams();
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
	const draftId = params.get(DRAFT_PARAM);
	const view = params.get(VIEW_PARAM) === MAP_VIEW ? MAP_VIEW : TABLE_VIEW;
	const setView = (next: string) => {
		const p = new URLSearchParams(params);
		if (next === MAP_VIEW) p.set(VIEW_PARAM, MAP_VIEW);
		else p.delete(VIEW_PARAM);
		setParams(p);
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
		if (view === MAP_VIEW && active !== undefined) {
			return (
				<ResourcesMap
					orgId={orgId}
					workspaceId={workspaceId}
					env={active}
					onShowTable={() => {
						setView(TABLE_VIEW);
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
			{params.has(NEW_RESOURCE_PARAM) && !envsLoading && (
				<NewService
					key={active?.id}
					orgId={orgId}
					workspaceId={workspaceId}
					env={active}
					onClose={() => {
						const next = new URLSearchParams(params);
						next.delete(NEW_RESOURCE_PARAM);
						setParams(next);
					}}
					onCreated={(id) => {
						setParams(new URLSearchParams(draftSearch(params, id)));
					}}
				/>
			)}
			{draftId !== null && active !== undefined && (
				<DraftSheet key={draftId} orgId={orgId} workspaceId={workspaceId} env={active} draftId={draftId} />
			)}
		</Page>
	);
}
