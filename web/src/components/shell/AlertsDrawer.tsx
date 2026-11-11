import { useQuery } from "@tanstack/react-query";
import { BellIcon, ScrollTextIcon } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { ResourceStatusBadge } from "@/components/design/StatusBadge";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import { type Alert, alertedResourceCount, buildAlerts } from "@/lib/alerts";
import { observabilityPath, resourcePath } from "@/lib/routes";
import { agoLabel } from "@/lib/time";

const CLOCK_TICK_MS = 60_000;

function AlertCard({
	alert,
	resourceHref,
	logsHref,
	nowMs,
	onNavigate,
}: {
	alert: Alert;
	resourceHref: string;
	logsHref: string;
	nowMs: number;
	onNavigate: () => void;
}) {
	return (
		<li className="rounded-card bg-panel p-4">
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
				<Button size="sm" nativeButton={false} render={<Link to={resourceHref} onClick={onNavigate} />}>
					Open {alert.name}
				</Button>
				<Button size="sm" variant="outline" nativeButton={false} render={<Link to={logsHref} onClick={onNavigate} />}>
					<ScrollTextIcon className="text-fg3" />
					Logs
				</Button>
			</div>
		</li>
	);
}

export function AlertsDrawer() {
	const [open, setOpen] = useState(false);
	const nowMs = useNow(CLOCK_TICK_MS).getTime();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { active } = useEnvironments();
	const resources = useQuery({
		...useWorkspaceResourcesOptions(activeOrgId ?? "", activeWorkspaceId ?? ""),
		enabled: activeOrgId !== null && activeWorkspaceId !== null && active !== undefined,
	});

	if (activeOrgId === null || activeWorkspaceId === null || active === undefined) return null;

	const alerts = buildAlerts(resources.data?.resources ?? [], active.id);
	const count = alertedResourceCount(alerts);
	const envParams = { env: active.name };
	const close = () => {
		setOpen(false);
	};

	return (
		<>
			<Button
				variant="outline"
				aria-label={`Alerts, ${count.toString()} active`}
				aria-haspopup="dialog"
				aria-expanded={open}
				onClick={() => {
					setOpen(true);
				}}
				className="px-2.5"
			>
				<BellIcon aria-hidden />
				<span className="hidden md:inline">Alerts</span>
				{count > 0 && <Badge tone="count">{count}</Badge>}
			</Button>
			<Sheet open={open} onOpenChange={setOpen}>
				<SheetContent>
					<SheetHeader>
						<SheetTitle>Alerts</SheetTitle>
						<span className="font-mono text-[13px] text-fg3">{count}</span>
					</SheetHeader>
					<SheetBody className="p-4">
						{alerts.length === 0 ? (
							<p className="px-2 py-12 text-center text-[13.5px] text-fg3">No active alerts</p>
						) : (
							<ul className="flex flex-col gap-2.5">
								{alerts.map((a) => (
									<AlertCard
										key={a.key}
										alert={a}
										resourceHref={resourcePath(activeOrgId, activeWorkspaceId, a.resourceId)}
										logsHref={observabilityPath(activeOrgId, activeWorkspaceId, "logs", { ...envParams, r: a.name })}
										nowMs={nowMs}
										onNavigate={close}
									/>
								))}
							</ul>
						)}
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
