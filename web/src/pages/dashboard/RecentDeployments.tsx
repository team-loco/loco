import { ArrowRightIcon, RocketIcon } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Section } from "@/components/design/Page";
import { SoonTag } from "@/components/design/SoonTag";
import { DeploymentPhaseBadge } from "@/components/design/StatusBadge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { deploymentImage } from "@/lib/deployment-utils";
import { imageVersion } from "@/lib/image";
import { observabilityPath, resourcePath } from "@/lib/routes";
import { agoLabel, tsMs } from "@/lib/time";

import {
	ALL_DEPLOYMENTS,
	DEPLOYMENT_FILTERS,
	deploymentCounts,
	deploymentState,
	filterDeployments,
	withinWindow,
} from "./deploymentRows";
import { startedLabel } from "./format";

const COLUMNS = 7;

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
	const navigate = useNavigate();
	const [filter, setFilter] = useState(ALL_DEPLOYMENTS);
	const recent = withinWindow(deployments, nowMs);
	const counts = deploymentCounts(recent);
	const rows = filterDeployments(recent, filter);

	const title = (
		<>
			Deployments
			<Badge tone="count">{recent.length}</Badge>
		</>
	);
	const toolbar = (
		<ToggleGroup
			variant="segmented"
			aria-label="Filter deployments by status"
			className="max-w-full overflow-x-auto"
			value={[filter]}
			onValueChange={(v: string[]) => {
				if (v[0] !== undefined) setFilter(v[0]);
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
		<Section
			cardTitle={title}
			cardActions={
				<>
					{toolbar}
					{actions}
				</>
			}
		>
			{recent.length === 0 ? (
				<EmptyState icon={<RocketIcon />} title={`No deployments to ${envName} in the last 24 hours`} />
			) : (
				<Table className="min-w-[900px] table-fixed">
					<TableHeader>
						<TableRow>
							<TableHead className="w-[120px]">Status</TableHead>
							<TableHead className="w-[180px]">Resource</TableHead>
							<TableHead className="w-[110px]">Region</TableHead>
							<TableHead className="w-[140px]">Image</TableHead>
							<TableHead className="w-[130px]">
								<span className="flex items-center gap-2">
									Deployed by
									<SoonTag />
								</span>
							</TableHead>
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
							const href = resourcePath(orgId, workspaceId, resource.id);
							const createdMs = tsMs(d.createdAt);
							return (
								<TableRow key={d.id} className="cursor-pointer hover:bg-panel2" onClick={() => void navigate(href)}>
									<TableCell>
										<DeploymentPhaseBadge phase={d.status} superseded={deploymentState(d) === "superseded"} />
									</TableCell>
									<TableCell className="truncate">
										<Link
											to={href}
											className="text-[14px] font-medium text-foreground underline-offset-3 hover:underline"
											onClick={(e) => {
												e.stopPropagation();
											}}
										>
											{resource.name}
										</Link>
									</TableCell>
									<TableCell className="truncate font-mono text-[12px] text-fg2">{d.region}</TableCell>
									<TableCell className="truncate font-mono text-[12px]">{imageVersion(deploymentImage(d))}</TableCell>
									<TableCell className="text-fg4">—</TableCell>
									<TableCell className="truncate text-fg2" title={d.message}>
										{d.message}
									</TableCell>
									<TableCell
										className="text-right font-mono text-[12px] whitespace-nowrap text-fg3"
										title={startedLabel(createdMs)}
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
