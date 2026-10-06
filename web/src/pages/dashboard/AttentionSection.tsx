import { ChevronRightIcon } from "lucide-react";
import { Link } from "react-router";
import { RegionIntentStatus, ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { Section } from "@/components/design/Page";
import { ResourceStatusBadge } from "@/components/design/StatusBadge";
import { tsMs } from "@/lib/time";

import { deploymentImage, imageTag, shortAgo } from "./format";
import type { EnvResource } from "./useDashboardData";

interface AttentionItem {
	key: string;
	href: string;
	status: ResourceStatus;
	name: string;
	region: string;
	message: string;
	when: string;
}

function needsAttention(status: ResourceStatus): boolean {
	switch (status) {
		case ResourceStatus.DEGRADED:
			return true;
		case ResourceStatus.UNAVAILABLE:
			return true;
		case ResourceStatus.DEPLOYING:
			return true;
		case ResourceStatus.HEALTHY:
			return false;
		case ResourceStatus.SUSPENDED:
			return false;
		case ResourceStatus.UNSPECIFIED:
			return false;
	}
}

function isBadIntent(status: RegionIntentStatus): boolean {
	return status === RegionIntentStatus.DEGRADED || status === RegionIntentStatus.FAILED;
}

export function buildAttention(items: EnvResource[], hrefFor: (id: string) => string, nowMs: number): AttentionItem[] {
	const out: AttentionItem[] = [];
	for (const item of items) {
		const { resource, last } = item;
		if (item.neverDeployed || !needsAttention(resource.status)) continue;
		const badRegion = resource.regions.find((r) => isBadIntent(r.status));
		const region = badRegion?.region ?? last?.region ?? resource.regions[0]?.region ?? "";
		const deployMessage = last?.message ?? "";
		const tag = last !== undefined ? imageTag(deploymentImage(last)) : "";
		const message =
			resource.status === ResourceStatus.DEPLOYING
				? [tag, deployMessage].filter((s) => s !== "").join(" · ")
				: (badRegion?.lastError ?? deployMessage);
		const whenMs =
			resource.status === ResourceStatus.DEPLOYING ? tsMs(last?.createdAt) : tsMs(last?.updatedAt ?? resource.updatedAt);
		out.push({
			key: resource.id,
			href: hrefFor(resource.id),
			status: resource.status,
			name: resource.name,
			region,
			message,
			when: shortAgo(whenMs, nowMs),
		});
	}
	return out;
}

export function AttentionSection({ items }: { items: AttentionItem[] }) {
	if (items.length === 0) return null;
	return (
		<Section title="Needs attention" bodyClassName="overflow-x-auto">
			{items.map((a, i) => (
				<Link
					key={a.key}
					to={a.href}
					className={`grid min-w-[640px] grid-cols-[110px_170px_minmax(0,1fr)_70px_16px] items-center gap-4 px-4 py-3 text-foreground no-underline hover:bg-bg2 hover:no-underline ${i > 0 ? "border-t border-line" : ""}`}
				>
					<ResourceStatusBadge status={a.status} />
					<span className="truncate font-semibold">
						{a.name} <span className="font-normal text-fg3">{a.region}</span>
					</span>
					<span className="truncate text-fg2">{a.message}</span>
					<span className="text-right text-fg3">{a.when}</span>
					<ChevronRightIcon className="size-3.5 text-fg3" />
				</Link>
			))}
		</Section>
	);
}
