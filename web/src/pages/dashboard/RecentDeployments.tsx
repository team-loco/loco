import { deploymentMessage } from "@/hooks/deployment-watch";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { ArrowRightIcon, RocketIcon } from "lucide-react";
import { parseAsStringLiteral, useQueryState } from "nuqs";
import { Link, useNavigate } from "react-router";
import { useQuery } from "@connectrpc/connect-query";
import { keepPreviousData } from "@tanstack/react-query";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import {
	listDeployments,
	queryDeploymentBuckets,
} from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";

import { Button } from "@/components/design/Button";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { EmptyState } from "@/components/design/EmptyState";
import { ErrorState } from "@/components/design/ErrorState";
import { Section } from "@/components/design/Page";
import { DeploymentActor, DeploymentId } from "@/components/design/DeploymentOrigin";
import { DeploymentStatusBadge } from "@/components/design/StatusBadge";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { deploymentRegions, isInFlight } from "@/lib/deployment-utils";
import { observabilityPath, resourcePath } from "@/lib/routes";
import { agoLabel, DAY_MS, formatStarted, msToTimestamp, readyMs } from "@/lib/time";

import {
	ALL_DEPLOYMENTS,
	DEPLOYMENT_FILTERS,
	DEPLOYMENT_LIMIT,
	DEPLOYMENT_WINDOW_MS,
	deploymentCounts,
	deploymentState,
	filterStatuses,
} from "./deploymentRows";

const POLL_MS = 5000;
const IDLE_POLL_MS = 15_000;

interface DeploymentRow {
	deployment: Deployment;
}

function When({ deployment, nowMs }: { deployment: Deployment; nowMs: number }) {
	const timePref = useTimePreferences();
	const ready = readyMs(deployment);
	return (
		<span title={ready === undefined ? undefined : formatStarted(ready, timePref)}>
			{ready === undefined ? "—" : agoLabel(ready, nowMs)}
		</span>
	);
}

function deploymentColumns(hrefOf: (row: DeploymentRow) => string, nowMs: number): DataColumn<DeploymentRow>[] {
	return [
		{
			id: "status",
			header: "Status",
			size: 120,
			minSize: 90,
			cell: ({ row: { original: r } }) => (
				<DeploymentStatusBadge
					phase={r.deployment.status}
					superseded={deploymentState(r.deployment) === "superseded"}
				/>
			),
		},
		{
			id: "resource",
			header: "Resource",
			size: 160,
			minSize: 90,
			meta: { primary: true },
			cell: ({ row: { original: r } }) => (
				<CellText
					text={r.deployment.resourceName}
					render={<Link to={hrefOf(r)} />}
					className="text-[14px] font-medium text-foreground"
				/>
			),
		},
		{
			id: "region",
			header: "Region",
			size: 110,
			minSize: 80,
			cell: ({ row: { original: r } }) => (
				<CellText text={deploymentRegions(r.deployment).join(", ")} className="font-mono text-[12px] text-fg2" />
			),
		},
		{
			id: "deployment",
			header: "Deployment",
			size: 130,
			minSize: 90,
			cell: ({ row: { original: r } }) => <DeploymentId deployment={r.deployment} />,
		},
		{
			id: "actor",
			header: "Deployed by",
			size: 170,
			minSize: 90,
			cell: ({ row: { original: r } }) => <DeploymentActor deployment={r.deployment} />,
		},
		{
			id: "message",
			header: "Message",
			size: 90,
			minSize: 90,
			meta: { grow: true },
			cell: ({ row: { original: r } }) => <CellText text={deploymentMessage(r.deployment)} className="text-fg2" />,
		},
		{
			id: "when",
			header: "When",
			size: 80,
			minSize: 64,
			meta: { align: "end", cellClassName: "font-mono text-[12px] whitespace-nowrap text-fg3" },
			cell: ({ row: { original: r } }) => <When deployment={r.deployment} nowMs={nowMs} />,
		},
	];
}

const DEPLOYS_VALUES = [ALL_DEPLOYMENTS, ...DEPLOYMENT_FILTERS.map((f) => f.value)];
const deploysParser = parseAsStringLiteral(DEPLOYS_VALUES)
	.withDefault(ALL_DEPLOYMENTS)
	.withOptions({ history: "replace" });

export function RecentDeployments({
	orgId,
	workspaceId,
	environmentId,
	envName,
	nowMs,
}: {
	orgId: string;
	workspaceId: string;
	environmentId: string;
	envName: string;
	nowMs: number;
}) {
	const navigate = useNavigate();
	const [filter, setFilter] = useQueryState("deploys", deploysParser);
	const since = msToTimestamp(nowMs - DEPLOYMENT_WINDOW_MS);
	const listed = useQuery(
		listDeployments,
		{ workspaceId, environmentId, since, pageSize: DEPLOYMENT_LIMIT, statuses: filterStatuses(filter) },
		{
			placeholderData: keepPreviousData,
			refetchInterval: (q) => (q.state.data?.deployments.some((d) => isInFlight(d.status)) ? POLL_MS : IDLE_POLL_MS),
		},
	);
	const counted = useQuery(
		queryDeploymentBuckets,
		{ workspaceId, environmentId, since, until: msToTimestamp(nowMs), bucketCount: 1 },
		{ placeholderData: keepPreviousData, refetchInterval: IDLE_POLL_MS },
	);
	const bucket = counted.data?.buckets[0];
	const total = counted.data === undefined ? undefined : (bucket?.deploymentCount ?? 0);
	const counts = deploymentCounts(bucket?.statusCounts ?? []);
	const rows = (listed.data?.deployments ?? []).map((deployment) => ({ deployment }));
	const error = listed.error ?? counted.error;
	const hrefOf = (row: DeploymentRow) => resourcePath(orgId, workspaceId, row.deployment.resourceId, { env: envName });
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
			render={<Link to={observabilityPath(orgId, workspaceId, "events", { env: envName, range: DAY_MS })} />}
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
				<EmptyState icon={<RocketIcon />} title={`No deployments to ${envName} in the last 24 hours`} />
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
