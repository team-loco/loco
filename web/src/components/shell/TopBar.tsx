import { SearchIcon } from "lucide-react";
import { Fragment } from "react";
import { matchPath, useLocation } from "react-router";

import {
	Breadcrumb,
	BreadcrumbItem,
	BreadcrumbList,
	BreadcrumbPage,
	BreadcrumbSeparator,
} from "@/components/design/Breadcrumb";
import { Kbd } from "@/components/design/Kbd";
import { SidebarTrigger } from "@/components/design/Sidebar";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { cn } from "@/lib/utils";
import { useQuery } from "@connectrpc/connect-query";
import { getResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { getWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";

import { useSidebarPeek } from "./AppShell";

const STATIC_CRUMBS: readonly (readonly [string, string])[] = [
	["/org/:orgId/wks/:workspaceId", "Overview"],
	["/org/:orgId/wks/:workspaceId/settings", "Settings"],
	["/org/:orgId/team", "Team"],
	["/tokens", "Tokens"],
	["/profile", "Profile"],
	["/organizations", "Organizations"],
];

const OBSERVABILITY_CRUMBS = new Map([
	["logs", "Logs"],
	["metrics", "Metrics"],
	["traces", "Traces"],
	["events", "Events"],
]);

function observabilityCrumb(search: string): string {
	const view = new URLSearchParams(search).get("view") ?? "logs";
	return OBSERVABILITY_CRUMBS.get(view) ?? "Logs";
}

function staticCrumb(pathname: string, search: string): string | null {
	if (matchPath("/org/:orgId/wks/:workspaceId/observability", pathname) !== null) return observabilityCrumb(search);
	for (const [pattern, label] of STATIC_CRUMBS) {
		if (matchPath(pattern, pathname) !== null) return label;
	}
	return null;
}

export function TopBar() {
	const { pathname, search } = useLocation();
	const { orgs, activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const resourceId = matchPath("/org/:orgId/wks/:workspaceId/resource/:resourceId", pathname)?.params.resourceId ?? "";
	const { data: resourceRes } = useQuery(
		getResource,
		{ key: { case: "resourceId", value: resourceId } },
		{ enabled: resourceId !== "" },
	);
	const pageCrumb = resourceId !== "" ? resourceRes?.resource?.name : staticCrumb(pathname, search);
	const { pinned } = useSidebarPeek();
	const { active: env } = useEnvironments();
	const { data: wsRes } = useQuery(
		getWorkspace,
		{ workspaceId: activeWorkspaceId ?? "" },
		{ enabled: activeWorkspaceId !== null },
	);
	const org = orgs.find((o) => o.id === activeOrgId);
	const inWorkspace = pathname.includes("/wks/");
	const showEnv = inWorkspace && !pathname.endsWith("/settings");

	const trail: string[] = [];
	if (org !== undefined) trail.push(org.name);
	if (inWorkspace && wsRes?.workspace !== undefined) trail.push(wsRes.workspace.name);
	if (showEnv && env !== undefined) trail.push(env.name);
	if (pageCrumb !== undefined && pageCrumb !== null) trail.push(pageCrumb);

	return (
		<div className="flex h-14 shrink-0 items-center gap-3 px-5 text-md">
			<SidebarTrigger
				aria-pressed={pinned}
				className={cn("size-7 rounded-sm hover:bg-bg3", pinned && "bg-info-bg text-primary hover:bg-info-bg hover:text-primary")}
			/>
			<span className="h-4 w-px bg-line" />
			<Breadcrumb className="min-w-0 overflow-hidden">
				<BreadcrumbList>
					{trail.map((c, i) => (
						<Fragment key={`${i}-${c}`}>
							{i > 0 && <BreadcrumbSeparator />}
							<BreadcrumbItem className="min-w-0">
								{i === trail.length - 1 ? <BreadcrumbPage>{c}</BreadcrumbPage> : <span className="truncate">{c}</span>}
							</BreadcrumbItem>
						</Fragment>
					))}
				</BreadcrumbList>
			</Breadcrumb>
			<div className="flex-1" />
			<div
				aria-disabled
				title="Search (soon)"
				className="hidden h-8 w-[280px] cursor-not-allowed items-center gap-2 rounded-sm border border-line px-2.5 text-base text-fg4 md:flex"
			>
				<SearchIcon className="size-3.5" />
				<span className="flex-1">Search</span>
				<span className="text-xs">soon</span>
				<Kbd>⌘K</Kbd>
			</div>
		</div>
	);
}
