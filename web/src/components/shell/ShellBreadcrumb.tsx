import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { CheckIcon, ChevronDownIcon, ChevronsUpDownIcon, PlusIcon, SettingsIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { matchPath, useLocation, useNavigate } from "react-router";

import type { Organization } from "@gen/loco/org/v1/org_pb";
import { getResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";

import {
	Breadcrumb,
	BreadcrumbItem,
	BreadcrumbList,
	BreadcrumbPage,
	BreadcrumbSeparator,
} from "@/components/design/Breadcrumb";
import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuGroup,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { environmentTypeLabel, useEnvironments } from "@/hooks/useEnvironment";
import { workspacePath } from "@/lib/routes";
import { cn } from "@/lib/utils";

import { useSidebarPeek } from "./AppShell";
import { CreateScopeDialog, type ScopeKind } from "./CreateScopeDialog";

const WORKSPACE_ROUTE = "/org/:orgId/wks/:workspaceId/*";

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

function staticCrumb(pathname: string, search: string): string | null {
	if (matchPath("/org/:orgId/wks/:workspaceId/observability", pathname) !== null) {
		const view = new URLSearchParams(search).get("view") ?? "logs";
		return OBSERVABILITY_CRUMBS.get(view) ?? "Logs";
	}
	for (const [pattern, label] of STATIC_CRUMBS) {
		if (matchPath(pattern, pathname) !== null) return label;
	}
	return null;
}

function switchWorkspacePath(pathname: string, search: string, orgId: string, workspaceId: string): string {
	const rest = matchPath(WORKSPACE_ROUTE, pathname)?.params["*"] ?? "";
	if (rest === "" || rest.startsWith("resource/")) return workspacePath(orgId, workspaceId);
	const params = new URLSearchParams(search);
	params.delete("env");
	const query = params.toString();
	return `${workspacePath(orgId, workspaceId, rest)}${query === "" ? "" : `?${query}`}`;
}

const TRIGGER =
	"flex h-7 min-w-0 items-center gap-1.5 rounded-sm px-1.5 text-md text-foreground outline-none transition-colors duration-150 ease-out hover:bg-bg3 focus-visible:ring-2 focus-visible:ring-ring/50 aria-expanded:bg-bg3";

function Slash() {
	return <BreadcrumbSeparator className="px-0.5 text-lg font-light text-line2">/</BreadcrumbSeparator>;
}

function Initial({ name }: { name: string }) {
	return (
		<span className="flex size-[18px] shrink-0 items-center justify-center rounded-sm bg-bg3 text-[10px] font-semibold text-fg2">
			{(name[0] ?? "?").toUpperCase()}
		</span>
	);
}

function CrumbMenu({
	title,
	label,
	heading,
	children,
}: {
	title: string;
	label: string;
	heading: string;
	children: ReactNode;
}) {
	const peek = useSidebarPeek();
	return (
		<BreadcrumbItem className="min-w-0">
			<DropdownMenu onOpenChange={peek.lock}>
				<DropdownMenuTrigger title={title} className={cn(TRIGGER, "max-w-[280px]")}>
					<span className="truncate">{label}</span>
					<ChevronDownIcon className="size-3.5 shrink-0 text-fg4" />
				</DropdownMenuTrigger>
				<DropdownMenuContent className="w-[260px]" align="start">
					<DropdownMenuGroup>
						<DropdownMenuLabel>{heading}</DropdownMenuLabel>
						{children}
					</DropdownMenuGroup>
				</DropdownMenuContent>
			</DropdownMenu>
		</BreadcrumbItem>
	);
}

function orgSettingsPath(orgId: string, workspaceId: string): string {
	return `${workspacePath(orgId, workspaceId, "settings")}?tab=org`;
}

function ScopeSwitcher({
	org,
	workspace,
	onCreate,
}: {
	org: Organization;
	workspace: Workspace | undefined;
	onCreate: (kind: ScopeKind) => void;
}) {
	const { pathname, search } = useLocation();
	const navigate = useNavigate();
	const peek = useSidebarPeek();
	const transport = useTransport();
	const { orgs, activeWorkspaceId } = useOrgWorkspace();
	const [open, setOpen] = useState(false);
	const results = useQueries({
		queries: orgs.map((o) => createQueryOptions(listOrgWorkspaces, { orgId: o.id }, { transport })),
	});

	const go = (to: string) => {
		setOpen(false);
		void navigate(to);
	};

	return (
		<BreadcrumbItem className="min-w-0">
			<DropdownMenu
				open={open}
				onOpenChange={(next: boolean) => {
					setOpen(next);
					peek.lock(next);
				}}
			>
				<DropdownMenuTrigger title="Switch workspace" className={cn(TRIGGER, "max-w-[360px] text-fg2")}>
					<span className="truncate">{org.name}</span>
					{workspace !== undefined && (
						<>
							<span className="text-fg4">/</span>
							<span className="truncate font-medium text-foreground">{workspace.name}</span>
						</>
					)}
					<ChevronsUpDownIcon className="size-3.5 shrink-0 text-fg3" />
				</DropdownMenuTrigger>
				<DropdownMenuContent className="w-[264px]" align="start">
					{orgs.map((o, i) => {
						const orgWorkspaces = results[i]?.data?.workspaces ?? [];
						const first = orgWorkspaces[0];
						return (
							<DropdownMenuGroup key={o.id}>
								<DropdownMenuLabel className="flex items-center justify-between pr-1">
									<span className="truncate">{o.name}</span>
									{first !== undefined && (
										<Button
											variant="ghost"
											size="icon-xs"
											title="Organization settings"
											aria-label={`${o.name} settings`}
											className="text-fg3"
											onClick={() => {
												go(orgSettingsPath(o.id, first.id));
											}}
										>
											<SettingsIcon />
										</Button>
									)}
								</DropdownMenuLabel>
								{orgWorkspaces.map((w) => (
									<DropdownMenuItem
										key={w.id}
										className={cn(w.id === activeWorkspaceId && "bg-bg3")}
										onClick={() => {
											if (w.id !== activeWorkspaceId) void navigate(switchWorkspacePath(pathname, search, o.id, w.id));
										}}
									>
										<Initial name={w.name} />
										<span className="flex-1 truncate">{w.name}</span>
										{w.id === activeWorkspaceId && <CheckIcon className="size-3.5" />}
									</DropdownMenuItem>
								))}
							</DropdownMenuGroup>
						);
					})}
					<DropdownMenuSeparator />
					<DropdownMenuItem
						onClick={() => {
							onCreate("workspace");
						}}
					>
						<PlusIcon className="text-fg3" />
						New workspace
					</DropdownMenuItem>
					<DropdownMenuItem
						onClick={() => {
							onCreate("org");
						}}
					>
						<PlusIcon className="text-fg3" />
						New organization
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
		</BreadcrumbItem>
	);
}

export function ShellBreadcrumb() {
	const { pathname, search } = useLocation();
	const { orgs, workspaces, activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { environments, active: env, setActive: setEnv } = useEnvironments();
	const [creating, setCreating] = useState<ScopeKind | null>(null);

	const resourceId = matchPath("/org/:orgId/wks/:workspaceId/resource/:resourceId", pathname)?.params.resourceId ?? "";
	const { data: resourceRes } = useQuery(
		getResource,
		{ key: { case: "resourceId", value: resourceId } },
		{ enabled: resourceId !== "" },
	);
	const pageCrumb = resourceId !== "" ? resourceRes?.resource?.name : staticCrumb(pathname, search);

	const org = orgs.find((o) => o.id === activeOrgId);
	const workspace = workspaces.find((w) => w.id === activeWorkspaceId);
	const inWorkspace = matchPath(WORKSPACE_ROUTE, pathname) !== null;
	const showEnv = inWorkspace && !pathname.endsWith("/settings");

	return (
		<Breadcrumb className="min-w-0 overflow-hidden">
			<BreadcrumbList className="gap-0 sm:gap-0">
				{org !== undefined && (
					<ScopeSwitcher
						org={org}
						workspace={workspace}
						onCreate={(kind) => {
							setCreating(kind);
						}}
					/>
				)}

				{showEnv && env !== undefined && (
					<>
						<Slash />
						<CrumbMenu
							title="Environment"
							heading="Environments"
							label={env.name}
						>
							{environments.map((e) => (
								<DropdownMenuItem
									key={e.id}
									onClick={() => {
										setEnv(e);
									}}
								>
									<span className="flex-1 truncate">{e.name}</span>
									<span className="text-sm text-fg3">{environmentTypeLabel(e.type)}</span>
									{e.id === env.id && <CheckIcon className="size-3.5" />}
								</DropdownMenuItem>
							))}
						</CrumbMenu>
					</>
				)}

				{pageCrumb !== undefined && pageCrumb !== null && (
					<>
						<Slash />
						<BreadcrumbItem className="min-w-0">
							<BreadcrumbPage className="px-2 font-medium">{pageCrumb}</BreadcrumbPage>
						</BreadcrumbItem>
					</>
				)}
			</BreadcrumbList>
			<CreateScopeDialog
				kind={creating}
				onOpenChange={(open) => {
					if (!open) setCreating(null);
				}}
			/>
		</Breadcrumb>
	);
}
