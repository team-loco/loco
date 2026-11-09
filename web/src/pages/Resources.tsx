import { Badge } from "@/components/design/Badge";
import { EmptyState } from "@/components/design/EmptyState";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { useWorkspaceDeployments } from "@/hooks/useWorkspaceDeployments";
import { getErrorMessage } from "@/lib/error-handler";
import { newResourcePath } from "@/lib/routes";
import { NewResourceMenu } from "@/pages/resources/NewResourceMenu";
import { ResourcesEmpty } from "@/pages/resources/ResourcesEmpty";
import { ResourceTable } from "@/pages/resources/ResourceTable";
import { buildRows } from "@/pages/resources/table";

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

	const orgId = activeOrgId ?? "";
	const workspaceId = activeWorkspaceId ?? "";
	const loading = envsLoading || data.isLoading;
	const total = data.resources.length;
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
		return (
			<ResourceTable
				rows={buildRows(data.resources, data.deployments, environments)}
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
		</Page>
	);
}
