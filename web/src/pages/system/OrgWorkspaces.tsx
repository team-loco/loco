import { keepPreviousData } from "@tanstack/react-query";
import { useShownPage } from "@/hooks/useTokenPage";
import { create } from "@bufbuild/protobuf";
import { useQuery } from "@connectrpc/connect-query";
import { listWorkspaceAllocations } from "@gen/loco/cluster/v1/cluster-ClusterAdminService_connectquery";
import { WorkspaceSchema, type Workspace } from "@gen/loco/workspace/v1/workspace_pb";
import { parseAsBoolean, parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { useNavigate } from "react-router";
import type { DataColumn } from "@/components/design/DataTable";
import { ErrorState } from "@/components/design/ErrorState";
import { Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { Skeleton } from "@/components/design/Skeleton";
import { pageParser, pageRangeLabel } from "@/lib/paging";
import { resourcesPath } from "@/lib/routes";
import { WorkspaceTable } from "@/pages/settings/OrgTab";
import { ReservationCoverage } from "@/components/ReservationCoverage";
import { reservationLabel } from "@/lib/reservation";

const PAGE_SIZE = 25;
const PARAMS = {
	wq: parseAsString.withDefault(""),
	wpage: pageParser.withDefault(0),
	wsort: parseAsStringLiteral(["cpu", "memory"]).withDefault("cpu"),
	wdesc: parseAsBoolean.withDefault(true),
};
export function OrgWorkspaces({ orgId }: { orgId: string }) {
	const [params, setParams] = useQueryStates(PARAMS, { history: "replace" });
	const navigate = useNavigate();
	const result = useQuery(
		listWorkspaceAllocations,
		{ orgId, query: params.wq, page: params.wpage, memory: params.wsort === "memory", ascending: !params.wdesc },
		{ refetchInterval: 30_000, placeholderData: keepPreviousData },
	);
	const allocations = new Map((result.data?.workspaces ?? []).map((row) => [row.workspaceId, row]));
	const rows = (result.data?.workspaces ?? []).map((row) =>
		create(WorkspaceSchema, {
			id: row.workspaceId,
			name: row.name,
			orgId: row.orgId,
			resourceCount: row.resourceCount,
		}),
	);
	const coverage = result.data?.coverage;
	const columns = ([false, true] as const).map((memory): DataColumn<Workspace> => ({
		id: memory ? "memory" : "cpu",
		header: memory ? "Reserved memory" : "Reserved CPU",
		size: 170,
		minSize: 110,
		meta: { align: "end", cellClassName: "font-mono text-[12px]" },
		accessorFn: (row) => Number(allocations.get(row.id)?.[memory ? "memoryBytes" : "cpuMillicores"] ?? 0n),
		cell: ({ row: { original } }) => {
			const reservation = allocations.get(original.id)!;
			const value = memory ? reservation.memoryBytes : reservation.cpuMillicores;
			return reservationLabel(value, memory, coverage);
		},
	}));
	const total = result.data?.totalCount ?? 0;
	const lastPage = Math.max(0, Math.ceil(total / PAGE_SIZE) - 1);
	const shown = useShownPage(
		params.wpage,
		rows.length === 0 ? `0 of ${total}` : pageRangeLabel(params.wpage, PAGE_SIZE, total),
		result.isPlaceholderData,
	);
	return (
		<Section
			id="workspaces"
			title="Workspaces"
			count={result.data?.totalCount.toLocaleString()}
			search={
				<SearchInput
					aria-label="Search workspaces"
					placeholder="Search workspaces"
					value={params.wq}
					onChange={(event) => {
						void setParams({ wq: event.target.value, wpage: 0 });
					}}
					onClear={() => {
						void setParams({ wq: "", wpage: 0 });
					}}
				/>
			}
		>
			<ReservationCoverage coverage={coverage} />
			{result.isLoading && <Skeleton className="m-5 h-40" />}
			{result.error !== null && (
				<div className="p-5">
					<ErrorState
						error={result.error}
						what="workspace reservations"
						rpc={listWorkspaceAllocations}
						onRetry={async () => await result.refetch()}
					/>
				</div>
			)}
			{result.data !== undefined && (
				<>
					<div aria-busy={result.isFetching}>
						<WorkspaceTable
							orgId={orgId}
							currentWorkspaceId=""
							rows={rows}
							allocationColumns={columns}
							hrefForWorkspace={(workspace) => resourcesPath(orgId, workspace.id)}
							onOpenWorkspace={(workspace) => {
								void navigate(resourcesPath(orgId, workspace.id));
							}}
							sort={{
								id: params.wsort,
								desc: params.wdesc,
								onChange: (id, desc) => {
									if (id === "cpu" || id === "memory") void setParams({ wsort: id, wdesc: desc, wpage: 0 });
								},
							}}
						/>
					</div>
					<Pager
						{...shown}
						total={total}
						pageSizes={[PAGE_SIZE]}
						loading={result.isFetching}
						onPrev={
							params.wpage === 0
								? null
								: () => {
										void setParams({ wpage: Math.min(params.wpage - 1, lastPage) });
									}
						}
						onNext={
							(params.wpage + 1) * PAGE_SIZE >= total
								? null
								: () => {
										void setParams({ wpage: params.wpage + 1 });
									}
						}
					/>
				</>
			)}
		</Section>
	);
}
