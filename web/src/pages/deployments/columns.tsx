import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { Link } from "react-router";
import { deploymentMessage } from "@/hooks/deployment-watch";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { CellText, type DataColumn } from "@/components/design/DataTable";
import { DeploymentActor, DeploymentId } from "@/components/design/DeploymentOrigin";
import { DeploymentStatusBadge } from "@/components/design/StatusBadge";
import { deploymentRegions } from "@/lib/deployment-utils";
import { agoLabel, formatStarted, readyMs } from "@/lib/time";
import { deploymentState } from "@/pages/dashboard/deploymentRows";

export interface DeploymentRow {
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

export function deploymentColumns(hrefOf: (row: DeploymentRow) => string, nowMs: number): DataColumn<DeploymentRow>[] {
	return [
		{
			id: "status",
			header: "Status",
			size: 120,
			minSize: 90,
			cell: ({ row: { original: r } }) => (
				<DeploymentStatusBadge
					phase={r.deployment.status}
					superseded={deploymentState(r.deployment.status) === "superseded"}
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
			meta: {
				align: "end",
				cellClassName: "font-mono text-[12px] whitespace-nowrap text-fg3",
			},
			cell: ({ row: { original: r } }) => <When deployment={r.deployment} nowMs={nowMs} />,
		},
	];
}
