import {
	ArrowUpRightIcon,
	BracesIcon,
	ChartLineIcon,
	LayoutDashboardIcon,
	ScrollTextIcon,
	Settings2Icon,
	WaypointsIcon,
} from "lucide-react";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { cn } from "@/lib/utils";

export type ResourceTab = "overview" | "variables" | "settings";

export function parseTab(value: string | null): ResourceTab {
	if (value === "variables") return "variables";
	if (value === "settings") return "settings";
	return "overview";
}

const TABS: { value: ResourceTab; label: string; icon: typeof LayoutDashboardIcon }[] = [
	{ value: "overview", label: "Overview", icon: LayoutDashboardIcon },
	{ value: "variables", label: "Variables", icon: BracesIcon },
	{ value: "settings", label: "Settings", icon: Settings2Icon },
];

export interface ObsLinks {
	logs: string;
	metrics: string;
	traces: string;
}

export function ResourceToolbar({
	tab,
	onTab,
	domain,
	obs,
	canRedeploy,
	redeploying,
	onRedeploy,
}: {
	tab: ResourceTab;
	onTab: (tab: ResourceTab) => void;
	domain: string | undefined;
	obs: ObsLinks | undefined;
	canRedeploy: boolean;
	redeploying: boolean;
	onRedeploy: () => void;
}) {
	const obsItems = obs === undefined
		? []
		: [
				{ label: "Logs", icon: ScrollTextIcon, href: obs.logs },
				{ label: "Metrics", icon: ChartLineIcon, href: obs.metrics },
				{ label: "Traces", icon: WaypointsIcon, href: obs.traces },
			];
	return (
		<div className="flex flex-wrap items-center gap-2.5 border-b border-line pb-4">
			<ToggleGroup
				variant="segmented"
				value={[tab]}
				onValueChange={(v: string[]) => {
					const next = v[0];
					if (next !== undefined) onTab(parseTab(next));
				}}
				className="rounded-xl"
			>
				{TABS.map((t) => (
					<ToggleGroupItem key={t.value} value={t.value} className="h-8! gap-2 px-3.5 text-[13.5px]">
						<t.icon className="size-[15px] opacity-85" />
						{t.label}
					</ToggleGroupItem>
				))}
			</ToggleGroup>
			<div className="flex-1" />
			{domain !== undefined && (
				<Button
					variant="outline"
					render={<a href={`https://${domain}`} target="_blank" rel="noopener noreferrer" />}
					nativeButton={false}
				>
					{domain}
					<ArrowUpRightIcon className="text-fg3" />
				</Button>
			)}
			{obsItems.length > 0 && (
				<div className="flex overflow-hidden rounded-sm border border-line">
					{obsItems.map((o, i) => (
						<Link
							key={o.label}
							to={o.href}
							className={cn(
								"flex h-8 items-center gap-1.5 px-3 text-foreground no-underline hover:bg-bg3 hover:no-underline",
								i > 0 && "border-l border-line",
							)}
						>
							<o.icon className="size-3.5 text-fg3" />
							{o.label}
						</Link>
					))}
				</div>
			)}
			<Button disabled={!canRedeploy || redeploying} onClick={onRedeploy}>
				{redeploying ? "Redeploying…" : "Redeploy"}
			</Button>
		</div>
	);
}
