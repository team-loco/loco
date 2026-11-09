import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { BellIcon, ScrollTextIcon } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { ResourceStatusBadge } from "@/components/design/StatusBadge";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { type Alert, alertedResourceCount, buildAlerts } from "@/lib/alerts";
import { observabilityPath, resourcePath } from "@/lib/routes";
import { agoLabel } from "@/lib/time";

const CLOCK_TICK_MS = 60_000;

interface AlertLinks {
	resource: (alert: Alert) => string;
	logs: (alert: Alert) => string;
}

function AlertCard({
	alert,
	links,
	nowMs,
	onNavigate,
}: {
	alert: Alert;
	links: AlertLinks;
	nowMs: number;
	onNavigate: () => void;
}) {
	return (
		<li className="rounded-[10px] bg-panel p-4">
			<div className="flex items-center justify-between gap-3">
				<ResourceStatusBadge status={alert.status} />
				{alert.reportedMs !== undefined && (
					<span className="font-mono text-[11.5px] text-fg3">updated {agoLabel(alert.reportedMs, nowMs)}</span>
				)}
			</div>
			<div className="mt-3 flex items-baseline gap-2">
				<span className="text-base font-medium">{alert.name}</span>
				<span className="font-mono text-xs text-fg3">{alert.region}</span>
			</div>
			{alert.description !== "" && <p className="mt-0.5 text-[13px] text-fg3">{alert.description}</p>}
			{alert.message !== "" && (
				<p className="mt-3 rounded-md bg-background px-3 py-2.5 font-mono text-xs/[1.55] break-words">
					{alert.message}
				</p>
			)}
			<div className="mt-3.5 flex gap-2">
				<Button size="sm" nativeButton={false} render={<Link to={links.resource(alert)} onClick={onNavigate} />}>
					Open {alert.name}
				</Button>
				<Button
					size="sm"
					variant="outline"
					nativeButton={false}
					render={<Link to={links.logs(alert)} onClick={onNavigate} />}
				>
					<ScrollTextIcon className="text-fg3" />
					Logs
				</Button>
			</div>
		</li>
	);
}

function AlertList({ alerts, links, onNavigate }: { alerts: Alert[]; links: AlertLinks; onNavigate: () => void }) {
	const nowMs = useNow(CLOCK_TICK_MS).getTime();
	if (alerts.length === 0) return <p className="px-2 py-12 text-center text-[13.5px] text-fg3">No active alerts</p>;
	return (
		<ul className="flex flex-col gap-2.5">
			{alerts.map((a) => (
				<AlertCard key={a.key} alert={a} links={links} nowMs={nowMs} onNavigate={onNavigate} />
			))}
		</ul>
	);
}

export function AlertsDrawer() {
	const [open, setOpen] = useState(false);
	const transport = useTransport();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { active } = useEnvironments();
	const resources = useQuery({
		...workspaceResourcesOptions(transport, activeWorkspaceId ?? ""),
		enabled: activeWorkspaceId !== null && active !== undefined,
	});

	if (activeOrgId === null || activeWorkspaceId === null || active === undefined) return null;

	const alerts = buildAlerts(resources.data?.resources ?? [], active.id);
	const count = alertedResourceCount(alerts);
	const envParams = { env: active.name };
	const links: AlertLinks = {
		resource: (a) => resourcePath(activeOrgId, activeWorkspaceId, a.resourceId),
		logs: (a) => observabilityPath(activeOrgId, activeWorkspaceId, "logs", { ...envParams, r: a.name }),
	};
	const close = () => {
		setOpen(false);
	};

	return (
		<>
			<button
				type="button"
				aria-label={`Alerts, ${count.toString()} active`}
				aria-haspopup="dialog"
				aria-expanded={open}
				onClick={() => {
					setOpen(true);
				}}
				className="flex h-8 shrink-0 items-center gap-[7px] rounded-md border border-line bg-background pr-2.5 pl-[9px] text-[13px] text-fg2 outline-none transition-colors duration-150 hover:border-line2 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50"
			>
				<BellIcon aria-hidden className="size-3.5" />
				<span className="hidden md:inline">Alerts</span>
				{count > 0 && (
					<span className="flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-panel2 px-[5px] font-mono text-[11px] text-foreground">
						{count}
					</span>
				)}
			</button>
			<Sheet open={open} onOpenChange={setOpen}>
				<SheetContent>
					<SheetHeader>
						<SheetTitle>Alerts</SheetTitle>
						<span className="font-mono text-[13px] text-fg3">{count}</span>
					</SheetHeader>
					<SheetBody className="p-4">
						<AlertList alerts={alerts} links={links} onNavigate={close} />
					</SheetBody>
					<SheetFooter>
						<Button
							variant="link"
							nativeButton={false}
							render={
								<Link to={observabilityPath(activeOrgId, activeWorkspaceId, "events", envParams)} onClick={close} />
							}
						>
							All events →
						</Button>
					</SheetFooter>
				</SheetContent>
			</Sheet>
		</>
	);
}
