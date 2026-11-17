import { useTimePreferences } from "@/lib/use-time-preferences";
import { ArrowRightIcon, RocketIcon } from "lucide-react";
import { parseAsStringLiteral, useQueryState } from "nuqs";
import { Link, useNavigate } from "react-router";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Section } from "@/components/design/Page";
import { DeploymentActor, DeploymentCommit } from "@/components/design/DeploymentOrigin";
import { DeploymentPhaseBadge } from "@/components/design/StatusBadge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { observabilityPath, resourcePath } from "@/lib/routes";
import { agoLabel, formatStarted, tsMs } from "@/lib/time";

import {
	ALL_DEPLOYMENTS,
	DEPLOYMENT_FILTERS,
	deploymentCounts,
	deploymentState,
	filterDeployments,
	withinWindow,
} from "./deploymentRows";

const COLUMNS = 7;
const DEPLOYS_VALUES = [ALL_DEPLOYMENTS, ...DEPLOYMENT_FILTERS.map((f) => f.value)];
const deploysParser = parseAsStringLiteral(DEPLOYS_VALUES)
	.withDefault(ALL_DEPLOYMENTS)
	.withOptions({ history: "replace" });

export function RecentDeployments({
	deployments,
	orgId,
	workspaceId,
	envName,
	nowMs,
}: {
	deployments: { deployment: Deployment; resource: Resource }[];
	orgId: string;
	workspaceId: string;
	envName: string;
	nowMs: number;
}) {
	const timePref = useTimePreferences();
	const navigate = useNavigate();
	const [filter, setFilter] = useQueryState("deploys", deploysParser);
	const recent = withinWindow(deployments, nowMs);
	const counts = deploymentCounts(recent);
	const rows = filterDeployments(recent, filter);

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
			<ToggleGroupItem value={ALL_DEPLOYMENTS} count={recent.length}>
				All
			</ToggleGroupItem>
			{DEPLOYMENT_FILTERS.map((o) => (
				<ToggleGroupItem key={o.value} value={o.value} count={counts.get(o.value) ?? 0}>
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
			render={<Link to={observabilityPath(orgId, workspaceId, "events", { env: envName })} />}
		>
			View all
			<ArrowRightIcon />
		</Button>
	);

	return (
		<Section title="Deployments" count={recent.length} actions={actions} filters={toolbar}>
			{recent.length === 0 ? (
				<EmptyState icon={<RocketIcon />} title={`No deployments to ${envName} in the last 24 hours`} />
			) : (
				<Table className="min-w-[1000px] table-fixed">
					<TableHeader>
						<TableRow>
							<TableHead className="w-[120px]">Status</TableHead>
							<TableHead className="w-[180px]">Resource</TableHead>
							<TableHead className="w-[110px]">Region</TableHead>
							<TableHead className="w-[150px]">Commit</TableHead>
							<TableHead className="w-[200px]">Deployed by</TableHead>
							<TableHead>Message</TableHead>
							<TableHead className="w-[100px] text-right">When</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{rows.length === 0 && (
							<TableRow>
								<TableCell colSpan={COLUMNS} className="h-auto p-0 whitespace-normal">
									<EmptyState title="No deployments with this status" />
								</TableCell>
							</TableRow>
						)}
						{rows.map(({ deployment: d, resource }) => {
							const href = resourcePath(orgId, workspaceId, resource.id, { env: envName });
							const createdMs = tsMs(d.createdAt);
							return (
								<TableRow key={d.id} onClick={() => void navigate(href)}>
									<TableCell>
										<DeploymentPhaseBadge phase={d.status} superseded={deploymentState(d) === "superseded"} />
									</TableCell>
									<TableCell className="truncate">
										<Link
											to={href}
											className="text-[14px] font-medium text-foreground"
											onClick={(e) => {
												e.stopPropagation();
											}}
										>
											{resource.name}
										</Link>
									</TableCell>
									<TableCell className="truncate font-mono text-[12px] text-fg2">{d.region}</TableCell>
									<TableCell>
										<DeploymentCommit deployment={d} />
									</TableCell>
									<TableCell>
										<DeploymentActor deployment={d} />
									</TableCell>
									<TableCell className="truncate text-fg2" title={d.message}>
										{d.message}
									</TableCell>
									<TableCell
										className="text-right font-mono text-[12px] whitespace-nowrap text-fg3"
										title={formatStarted(createdMs, timePref)}
									>
										{agoLabel(createdMs, nowMs)}
									</TableCell>
								</TableRow>
							);
						})}
					</TableBody>
				</Table>
			)}
		</Section>
	);
}
