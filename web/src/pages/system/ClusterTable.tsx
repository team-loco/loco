import type { Cluster } from "@gen/loco/cluster/v1/cluster_pb";
import type { ReactNode } from "react";
import { useNavigate } from "react-router";

import { Badge } from "@/components/design/Badge";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { agentBehind, clusterPath, ROLE_LABEL, type ClusterState } from "@/lib/system";

import { CapacityCell } from "./Capacity";
import { ClusterRowMenu } from "./ClusterActions";
import { ClusterStateBadge, SystemTime } from "./shared";

export interface ClusterRow {
	cluster: Cluster;
	state: ClusterState;
}

export function AgentVersion({ cluster, apiVersion }: { cluster: Cluster; apiVersion: string }) {
	if (cluster.agentVersion === undefined) return <span className="text-fg3">None</span>;
	return (
		<span className="flex min-w-0 items-center gap-2">
			<CellText text={cluster.agentVersion} />
			{agentBehind(apiVersion, cluster.agentVersion) && (
				<Badge tone="warn" size="sm" title={`The API runs ${apiVersion}`}>
					Behind API
				</Badge>
			)}
		</span>
	);
}

function columns(apiVersion: string, canChange: boolean): DataColumn<ClusterRow>[] {
	return [
		{
			id: "name",
			header: "Cluster",
			size: 200,
			minSize: 120,
			meta: { primary: true, fit: true, fitMax: 280 },
			cell: ({ row: { original: r } }) => <CellText text={r.cluster.name} className="font-mono text-[13px]" />,
		},
		{
			id: "state",
			header: "State",
			size: 140,
			minSize: 110,
			cell: ({ row: { original: r } }) => <ClusterStateBadge state={r.state} />,
		},
		{
			id: "headroom",
			header: "Deployment headroom",
			size: 225,
			minSize: 180,
			cell: ({ row: { original: r } }) => <CapacityCell capacity={r.cluster.capacity} />,
		},
		{
			id: "role",
			header: "Role",
			size: 130,
			minSize: 90,
			meta: { hideOnMobile: true },
			cell: ({ row: { original: r } }) => <CellText text={ROLE_LABEL[r.cluster.role]} />,
		},
		{
			id: "agent",
			header: "Agent",
			size: 200,
			minSize: 120,
			meta: { grow: true },
			cell: ({ row: { original: r } }) => <AgentVersion cluster={r.cluster} apiVersion={apiVersion} />,
		},
		{
			id: "heartbeat",
			header: "Last heartbeat",
			size: 190,
			minSize: 150,
			meta: { hideOnMobile: true },
			cell: ({ row: { original: r } }) => <SystemTime ts={r.cluster.lastHeartbeat} />,
		},
		...(canChange
			? [
					{
						id: "menu",
						meta: { pinEnd: true, label: "Actions" },
						cell: ({ row: { original: r } }: { row: { original: ClusterRow } }) => (
							<ClusterRowMenu cluster={r.cluster} state={r.state} />
						),
					} satisfies DataColumn<ClusterRow>,
				]
			: []),
	];
}

/** Clusters as a table; with `byRegion` each region opens its own group. */
export function ClusterTable({
	rows,
	apiVersion,
	canChange,
	byRegion = false,
	loading = false,
	empty,
	label,
}: {
	rows: ClusterRow[];
	apiVersion: string;
	canChange: boolean;
	byRegion?: boolean;
	loading?: boolean;
	empty?: ReactNode;
	label: string;
}) {
	const navigate = useNavigate();
	const table = useDataTable("system-clusters", {
		columns: columns(apiVersion, canChange),
		data: rows,
		getRowId: (r) => r.cluster.id,
	});
	return (
		<DataTable
			table={table}
			label={label}
			band
			loading={loading}
			empty={empty}
			{...(byRegion ? { groupBy: (r: ClusterRow) => r.cluster.region } : {})}
			rowAction="navigate"
			rowLabel={(row) => `Open ${row.original.cluster.name}`}
			onRowClick={(row) => {
				void navigate(clusterPath(row.original.cluster.id));
			}}
		/>
	);
}
