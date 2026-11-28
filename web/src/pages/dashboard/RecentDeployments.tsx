import { ArrowRightIcon } from "lucide-react";
import { parseAsStringLiteral, useQueryState } from "nuqs";
import { Link, useNavigate } from "react-router";
import { useQuery } from "@connectrpc/connect-query";
import { keepPreviousData } from "@tanstack/react-query";
import {
	listDeployments,
	queryDeploymentBuckets,
} from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";

import { Button } from "@/components/design/Button";
import { DataTable, useDataTable } from "@/components/design/DataTable";
import { EmptyState } from "@/components/design/EmptyState";
import { ErrorState } from "@/components/design/ErrorState";
import { Section } from "@/components/design/Page";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { isInFlight } from "@/lib/deployment-utils";
import { resourcePath, withEnv, workspacePath } from "@/lib/routes";
import { msToTimestamp } from "@/lib/time";

import {
	ALL_DEPLOYMENTS,
	DEPLOYMENT_FILTERS,
	DEPLOYMENT_LIMIT,
	DEPLOYMENT_WINDOW_MS,
	deploymentCounts,
	filterStatuses,
} from "./deploymentRows";
import { InlineCode } from "@/components/design/InlineCode";
import { deploymentColumns, type DeploymentRow } from "@/pages/deployments/columns";

const POLL_MS = 5000;
const IDLE_POLL_MS = 15_000;

const DEPLOYS_VALUES = [ALL_DEPLOYMENTS, ...DEPLOYMENT_FILTERS.map((f) => f.value)];
const deploysParser = parseAsStringLiteral(DEPLOYS_VALUES)
	.withDefault(ALL_DEPLOYMENTS)
	.withOptions({ history: "replace" });

export function RecentDeployments({
	orgId,
	workspaceId,
	environmentId,
	envSlug,
	envName,
	nowMs,
}: {
	orgId: string;
	workspaceId: string;
	environmentId: string;
	envSlug: string;
	envName: string;
	nowMs: number;
}) {
	const navigate = useNavigate();
	const [filter, setFilter] = useQueryState("deploys", deploysParser);
	const startTime = msToTimestamp(nowMs - DEPLOYMENT_WINDOW_MS);
	const listed = useQuery(
		listDeployments,
		{ workspaceId, environmentId, startTime, pageSize: DEPLOYMENT_LIMIT, statuses: filterStatuses(filter) },
		{
			placeholderData: keepPreviousData,
			refetchInterval: (q) => (q.state.data?.deployments.some((d) => isInFlight(d.status)) ? POLL_MS : IDLE_POLL_MS),
		},
	);
	const counted = useQuery(
		queryDeploymentBuckets,
		{ workspaceId, environmentId, startTime, endTime: msToTimestamp(nowMs), bucketCount: 1 },
		{ placeholderData: keepPreviousData, refetchInterval: IDLE_POLL_MS },
	);
	const bucket = counted.data?.buckets[0];
	const total = counted.data === undefined ? undefined : (bucket?.deploymentCount ?? 0);
	const counts = deploymentCounts(bucket?.statusCounts ?? []);
	const rows = (listed.data?.deployments ?? []).map((deployment) => ({ deployment }));
	const error = listed.error ?? counted.error;
	const hrefOf = (row: DeploymentRow) => resourcePath(orgId, workspaceId, row.deployment.resourceId, { env: envSlug });
	const table = useDataTable("dashboard-deployments", {
		columns: deploymentColumns(hrefOf, nowMs),
		data: rows,
		getRowId: (row) => row.deployment.id,
	});

	const toolbar = (
		<ToggleGroup
			variant="segmented"
			aria-label="Filter deployments by status"
			className="max-w-full overflow-x-auto"
			value={[filter]}
			onValueChange={(v: string[]) => {
				const next = DEPLOYS_VALUES.find((f) => f === v[0]);
				if (next !== undefined) void setFilter(next);
			}}
		>
			<ToggleGroupItem value={ALL_DEPLOYMENTS} count={total}>
				All
			</ToggleGroupItem>
			{DEPLOYMENT_FILTERS.map((o) => (
				<ToggleGroupItem
					key={o.value}
					value={o.value}
					count={total === undefined ? undefined : (counts.get(o.value) ?? 0)}
				>
					{o.label}
				</ToggleGroupItem>
			))}
		</ToggleGroup>
	);
	const actions = (
		<Button
			variant="outline"
			size="sm"
			nativeButton={false}
			render={<Link to={`${withEnv(workspacePath(orgId, workspaceId, "deployments"), envSlug)}&deploys=${filter}`} />}
		>
			View all
			<ArrowRightIcon />
		</Button>
	);

	return (
		<Section title="Deployments" count={total} actions={actions} filters={toolbar}>
			{error !== null ? (
				<ErrorState
					error={error}
					what="deployments"
					rpc={listDeployments}
					onRetry={async () => await Promise.all([listed.refetch(), counted.refetch()])}
				/>
			) : total === 0 && rows.length === 0 ? (
				<EmptyState
					title={
						<>
							No deployments to <InlineCode>{envName}</InlineCode> in the last 24 hours
						</>
					}
				/>
			) : (
				<DataTable
					table={table}
					inset
					label="Deployments"
					loading={listed.isPending}
					className="[--row-h:66px]"
					onRowClick={(row) => void navigate(hrefOf(row.original))}
					rowAction="navigate"
					empty={<EmptyState title="No deployments with this status." />}
				/>
			)}
		</Section>
	);
}
