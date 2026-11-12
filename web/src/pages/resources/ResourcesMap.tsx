import { ArrowRightIcon, ScrollTextIcon, WaypointsIcon } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";

import { Button } from "@/components/design/Button";
import { Section } from "@/components/design/Page";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { ResourceStatusBadge } from "@/components/design/StatusBadge";
import { deploymentImage } from "@/lib/deployment-utils";
import { imageVersion } from "@/lib/image";
import { primaryDomain } from "@/lib/resource-status";
import { observabilityPath, resourcePath } from "@/lib/routes";
import { ArchitectureDiagram } from "@/pages/dashboard/ArchitectureDiagram";
import { isDraftItem, type DashboardItem } from "@/pages/dashboard/resourceRows";
import { useDashboardData, type EnvResource } from "@/pages/dashboard/useDashboardData";

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<div className="flex flex-col gap-1">
			<span className="text-[12px] text-fg3">{label}</span>
			<span className="font-mono text-[13px] break-all">{children}</span>
		</div>
	);
}

export function ResourcesMap({
	orgId,
	workspaceId,
	env,
	onShowTable,
}: {
	orgId: string;
	workspaceId: string;
	env: Environment;
	onShowTable: () => void;
}) {
	const data = useDashboardData(orgId, workspaceId, env);
	const [selectedId, setSelectedId] = useState<string | null>(null);
	const deployed = data.envResources.filter((r) => r.last !== undefined);
	const selected = deployed.find((r) => r.resource.id === selectedId);
	const envParam = { env: env.name };
	const version = selected?.last === undefined ? "—" : imageVersion(deploymentImage(selected.last));
	const hrefFor = (item: DashboardItem) =>
		isDraftItem(item)
			? ""
			: `${resourcePath(orgId, workspaceId, item.resource.id)}?env=${encodeURIComponent(env.name)}`;

	return (
		<Section>
			{deployed.length === 0 ? (
				<div className="py-10 text-center text-fg3">Nothing is deployed to {env.name} yet.</div>
			) : (
				<ArchitectureDiagram
					rows={deployed}
					regionOrder={data.regions.map((r) => r.region)}
					environmentId={env.id}
					hrefFor={hrefFor}
					onShowTable={onShowTable}
					onSelect={(item: EnvResource) => {
						setSelectedId(item.resource.id);
					}}
				/>
			)}
			<Sheet
				open={selected !== undefined}
				onOpenChange={(open) => {
					if (!open) setSelectedId(null);
				}}
			>
				{selected !== undefined && (
					<SheetContent>
						<SheetHeader>
							<SheetTitle className="truncate font-mono">{selected.resource.name}</SheetTitle>
							<ResourceStatusBadge status={selected.status} />
						</SheetHeader>
						<SheetBody>
							<Fact label="Version">{version}</Fact>
							<Fact label="Replicas">
								{selected.regions.map((g) => `${g.region} ${g.ready.toString()}/${g.desired.toString()}`).join(" · ")}
							</Fact>
							<Fact label="Address">{primaryDomain(selected.resource) ?? "internal only"}</Fact>
						</SheetBody>
						<SheetFooter className="justify-start">
							<Button
								variant="outline"
								nativeButton={false}
								render={
									<Link
										to={observabilityPath(orgId, workspaceId, "logs", { ...envParam, r: selected.resource.name })}
									/>
								}
							>
								<ScrollTextIcon />
								Logs
							</Button>
							<Button
								variant="outline"
								nativeButton={false}
								render={
									<Link
										to={observabilityPath(orgId, workspaceId, "traces", { ...envParam, r: selected.resource.name })}
									/>
								}
							>
								<WaypointsIcon />
								Traces
							</Button>
							<Button nativeButton={false} className="ml-auto" render={<Link to={hrefFor(selected)} />}>
								Open {selected.resource.name}
								<ArrowRightIcon />
							</Button>
						</SheetFooter>
					</SheetContent>
				)}
			</Sheet>
		</Section>
	);
}
