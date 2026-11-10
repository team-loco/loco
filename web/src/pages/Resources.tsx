import { useQuery } from "@connectrpc/connect-query";
import { useSearchParams } from "react-router";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";

import { Badge } from "@/components/design/Badge";
import { EmptyState } from "@/components/design/EmptyState";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { useDrafts } from "@/hooks/useDrafts";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { useWorkspaceDeployments } from "@/hooks/useWorkspaceDeployments";
import { getErrorMessage } from "@/lib/error-handler";
import { DRAFT_PARAM, draftSearch, NEW_RESOURCE_PARAM, newResourcePath } from "@/lib/routes";
import { NewResourceMenu } from "@/pages/resources/NewResourceMenu";
import { DraftSheet, NewService } from "@/pages/resources/NewService";
import { ResourcesEmpty } from "@/pages/resources/ResourcesEmpty";
import { ResourceTable } from "@/pages/resources/ResourceTable";
import { buildRows, draftRows } from "@/pages/resources/table";

const NOW_TICK_MS = 60_000;
const SKELETON_ROWS = 6;
const TABLE_VIEW = "table";
const MAP_VIEW = "map";

function ViewToggle() {
	return (
		<ToggleGroup variant="segmented" aria-label="View" value={[TABLE_VIEW]}>
			<ToggleGroupItem value={TABLE_VIEW}>Table</ToggleGroupItem>
			<ToggleGroupItem value={MAP_VIEW} disabled>
				Map
				<SoonTag />
			</ToggleGroupItem>
		</ToggleGroup>
	);
}

export function Resources() {
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { environments, active, isLoading: envsLoading } = useEnvironments();
	const data = useWorkspaceDeployments(activeWorkspaceId);
	const nowMs = useNow(NOW_TICK_MS).getTime();
	const [params, setParams] = useSearchParams();
	const orgId = activeOrgId ?? "";
	const workspaceId = activeWorkspaceId ?? "";
	const { drafts } = useDrafts(orgId, workspaceId, active?.id);
	const configQuery = useQuery(getConfig, {}, { enabled: drafts.length > 0 });

	const loading = envsLoading || data.isLoading;
	const total = data.resources.length + drafts.length;
	const draftId = params.get(DRAFT_PARAM);
	const serviceHref = newResourcePath(orgId, workspaceId, active === undefined ? {} : { env: active.name });

	const body = (() => {
		if (data.error !== null) {
			return (
				<Section>
					<EmptyState title="Couldn't load resources">
						{getErrorMessage(data.error, "Failed to load resources")}
					</EmptyState>
				</Section>
			);
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
		if (total === 0) return <ResourcesEmpty />;
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
						{!loading && <Badge tone="count">{total}</Badge>}
					</>
				}
				actions={
					<div className="flex items-center gap-2.5">
						{!loading && total > 0 && <ViewToggle />}
						<NewResourceMenu serviceHref={serviceHref} />
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
