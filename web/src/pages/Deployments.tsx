import { useState } from "react";
import { parseAsStringLiteral, useQueryState } from "nuqs";
import { useNavigate } from "react-router";
import { keepPreviousData } from "@tanstack/react-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";

import { DataTable, useDataTable } from "@/components/design/DataTable";
import { EmptyState } from "@/components/design/EmptyState";
import { ErrorState } from "@/components/design/ErrorState";
import { Page, PageHeader, PageLoading, Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { useShownPage, useTokenPage } from "@/hooks/useTokenPage";
import { isInFlight } from "@/lib/deployment-utils";
import { tokenPageLabel, tokenPager } from "@/lib/paging";
import { resourcePath } from "@/lib/routes";
import { ALL_DEPLOYMENTS, DEPLOYMENT_FILTERS, filterStatuses } from "./dashboard/deploymentRows";
import { deploymentColumns, type DeploymentRow } from "./deployments/columns";

const PAGE_SIZES = [25, 50, 100] as const;
const POLL_MS = 5000;
const IDLE_POLL_MS = 15_000;
const FILTERS = [ALL_DEPLOYMENTS, ...DEPLOYMENT_FILTERS.map((f) => f.value)];
const filterParser = parseAsStringLiteral(FILTERS).withDefault(ALL_DEPLOYMENTS).withOptions({ history: "replace" });

export function Deployments() {
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { active, isLoading } = useEnvironments();
	const [filter, setFilter] = useQueryState("deploys", filterParser);
	if (isLoading) return <PageLoading />;
	if (active === undefined)
		return (
			<Page>
				<PageHeader title="Deployments" />
				<Section>
					<EmptyState title="No environments yet" />
				</Section>
			</Page>
		);
	return (
		<History
			key={`${active.id}:${filter}`}
			orgId={activeOrgId ?? ""}
			workspaceId={activeWorkspaceId ?? ""}
			env={active}
			filter={filter}
			onFilter={(value) => {
				void setFilter(value);
			}}
		/>
	);
}

function History({
	orgId,
	workspaceId,
	env,
	filter,
	onFilter,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment;
	filter: string;
	onFilter: (value: string) => void;
}) {
	const [size, setSize] = useState<number>(PAGE_SIZES[0]);
	const [tokens, setTokens] = useState<string[]>([]);
	const nowMs = useNow(60_000).getTime();
	const navigate = useNavigate();
	const history = useTokenPage(
		listDeployments,
		{
			workspaceId,
			environmentId: env.id,
			pageSize: size,
			statuses: filterStatuses(filter),
		},
		tokens,
		{
			placeholderData: keepPreviousData,
			refetchInterval: (q) => (q.state.data?.deployments.some((d) => isInFlight(d.status)) ? POLL_MS : IDLE_POLL_MS),
		},
	);
	const deployments = history.query.data?.deployments ?? [];
	const pager = tokenPager(tokens, history.nextPageToken, (next) => {
		if (!history.query.isPlaceholderData) setTokens(next ?? []);
	});
	const shown = useShownPage(
		pager.page,
		tokenPageLabel(pager.page, size, deployments.length, history.total),
		history.query.isPlaceholderData,
	);
	const hrefOf = (row: DeploymentRow) =>
		resourcePath(orgId, workspaceId, row.deployment.resourceId, {
			env: env.slug,
		});
	const table = useDataTable("workspace-deployments", {
		columns: deploymentColumns(hrefOf, nowMs),
		data: deployments.map((deployment) => ({ deployment })),
		getRowId: (row) => row.deployment.id,
	});
	const filters = (
		<ToggleGroup
			variant="segmented"
			aria-label="Filter deployments by status"
			className="max-w-full overflow-x-auto"
			value={[filter]}
			onValueChange={(values: string[]) => {
				const value = FILTERS.find((candidate) => candidate === values[0]);
				if (value !== undefined) onFilter(value);
			}}
		>
			<ToggleGroupItem value={ALL_DEPLOYMENTS}>All</ToggleGroupItem>
			{DEPLOYMENT_FILTERS.map((option) => (
				<ToggleGroupItem key={option.value} value={option.value}>
					{option.label}
				</ToggleGroupItem>
			))}
		</ToggleGroup>
	);
	return (
		<Page>
			<PageHeader title="Deployments" />
			<Section filters={filters}>
				{history.error !== null ? (
					<ErrorState error={history.error} what="deployments" rpc={listDeployments} onRetry={history.query.refetch} />
				) : (
					<>
						<DataTable
							table={table}
							inset
							label="Deployments"
							loading={history.query.isPending}
							className="[--row-h:66px]"
							onRowClick={(row) => {
								void navigate(hrefOf(row.original));
							}}
							rowAction="navigate"
							empty={<EmptyState title="No deployments with this status." />}
						/>
						<Pager
							total={deployments.length}
							pageSizes={PAGE_SIZES}
							pageSize={size}
							onPageSize={(next) => {
								setSize(next);
								setTokens([]);
							}}
							{...shown}
							onFirst={pager.onFirst}
							onPrev={pager.onPrev}
							onNext={pager.onNext}
							loading={history.query.isFetching}
						/>
					</>
				)}
			</Section>
		</Page>
	);
}
