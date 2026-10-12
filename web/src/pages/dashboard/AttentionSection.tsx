import { ChevronRightIcon } from "lucide-react";
import { Link } from "react-router";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { Section } from "@/components/design/Page";
import { ResourceStatusBadge } from "@/components/design/StatusBadge";
import { imageVersion } from "@/lib/image";
import { tsMs } from "@/lib/time";

import { deploymentImage, shortAgo } from "./format";
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
		case ResourceStatus.UNKNOWN:
			return true;
		case ResourceStatus.HEALTHY:
			return false;
		case ResourceStatus.UNSPECIFIED:
			return false;
	}
}

export function buildAttention(items: EnvResource[], hrefFor: (id: string) => string, nowMs: number): AttentionItem[] {
	const out: AttentionItem[] = [];
	for (const item of items) {
		const { resource, last, status } = item;
		if (item.neverDeployed || !needsAttention(status)) continue;
		const badRegion = item.environment?.regions.find((r) => r.status !== ResourceStatus.HEALTHY);
		const region = badRegion?.region ?? last?.region ?? resource.regions[0]?.region ?? "";
		const deployMessage = last?.message ?? "";
		const tag = last !== undefined ? imageVersion(deploymentImage(last)) : "";
		const message =
			status === ResourceStatus.DEPLOYING
				? [tag, deployMessage].filter((s) => s !== "").join(" · ")
				: (badRegion?.message ?? deployMessage);
		const whenMs =
			status === ResourceStatus.DEPLOYING ? tsMs(last?.createdAt) : tsMs(last?.updatedAt ?? resource.updatedAt);
		out.push({
			key: resource.id,
			href: hrefFor(resource.id),
			status,
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
