import { SearchIcon } from "lucide-react";
import { Fragment } from "react";
import { useLocation } from "react-router";

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
import { useShellCrumbs } from "@/context/ShellContext";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useQuery } from "@connectrpc/connect-query";
import { getWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";

export function TopBar() {
	const { pathname } = useLocation();
	const { orgs, activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const crumbs = useShellCrumbs();
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
	trail.push(...crumbs);

	return (
		<div className="flex h-14 shrink-0 items-center gap-3 px-5 text-md">
			<SidebarTrigger className="size-7 rounded-sm hover:bg-bg3" />
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
