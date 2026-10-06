import { useQuery } from "@connectrpc/connect-query";
import { Building2Icon, CheckIcon, ChevronDownIcon, LayersIcon, PlusIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { matchPath, useLocation, useNavigate } from "react-router";

import { getResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import {
	Breadcrumb,
	BreadcrumbItem,
	BreadcrumbList,
	BreadcrumbPage,
	BreadcrumbSeparator,
} from "@/components/design/Breadcrumb";
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
import { environmentDotClass, environmentTypeLabel, useEnvironments } from "@/hooks/useEnvironment";
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
	"flex h-8 min-w-0 items-center gap-2 rounded-sm px-2 text-md text-foreground outline-none hover:bg-bg3 focus-visible:ring-2 focus-visible:ring-ring/50 aria-expanded:bg-bg3";

function Slash() {
	return <BreadcrumbSeparator className="px-0.5 text-lg font-light text-line2">/</BreadcrumbSeparator>;
}

function Initial({ name, strong }: { name: string; strong?: boolean }) {
	return (
		<span
			className={cn(
				"flex shrink-0 items-center justify-center rounded-sm font-semibold",
				strong === true ? "size-5 bg-foreground text-[11px] text-background" : "size-[18px] bg-bg3 text-[10px] text-fg2",
			)}
		>
			{(name[0] ?? "?").toUpperCase()}
		</span>
	);
}

function CrumbMenu({
	title,
	icon,
	label,
	heading,
	children,
}: {
	title: string;
	icon: ReactNode;
	label: string;
	heading: string;
	children: ReactNode;
}) {
	const peek = useSidebarPeek();
	return (
		<BreadcrumbItem className="min-w-0">
			<DropdownMenu onOpenChange={peek.lock}>
				<DropdownMenuTrigger title={title} className={cn(TRIGGER, "max-w-[280px]")}>
					{icon}
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

export function ShellBreadcrumb() {
	const { pathname, search } = useLocation();
	const navigate = useNavigate();
	const { orgs, workspaces, activeOrgId, activeWorkspaceId, setActiveOrg } = useOrgWorkspace();
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
					<CrumbMenu title="Organization" heading="Organizations" label={org.name} icon={<Initial name={org.name} strong />}>
						{orgs.map((o) => (
							<DropdownMenuItem
								key={o.id}
								onClick={() => {
									if (o.id !== activeOrgId) setActiveOrg(o.id);
								}}
							>
								<Initial name={o.name} />
								<span className="flex-1 truncate">{o.name}</span>
								{o.id === activeOrgId && <CheckIcon className="size-3.5" />}
							</DropdownMenuItem>
						))}
						<DropdownMenuSeparator />
						<DropdownMenuItem
							onClick={() => {
								void navigate("/organizations");
							}}
						>
							<Building2Icon className="text-fg3" />
							Manage organizations
						</DropdownMenuItem>
						<DropdownMenuItem
							onClick={() => {
								setCreating("org");
							}}
						>
							<PlusIcon className="text-fg3" />
							New organization
						</DropdownMenuItem>
					</CrumbMenu>
				)}

				{inWorkspace && org !== undefined && workspace !== undefined && (
					<>
						<Slash />
						<CrumbMenu
							title="Workspace"
							heading={`Workspaces in ${org.name}`}
							label={workspace.name}
							icon={<LayersIcon className="size-3.5 shrink-0 text-fg3" />}
						>
							{workspaces.map((w) => (
								<DropdownMenuItem
									key={w.id}
									onClick={() => {
										if (w.id !== activeWorkspaceId) void navigate(switchWorkspacePath(pathname, search, org.id, w.id));
									}}
								>
									<Initial name={w.name} />
									<span className="flex-1 truncate">{w.name}</span>
									{w.id === activeWorkspaceId && <CheckIcon className="size-3.5" />}
								</DropdownMenuItem>
							))}
							<DropdownMenuSeparator />
							<DropdownMenuItem
								onClick={() => {
									setCreating("workspace");
								}}
							>
								<PlusIcon className="text-fg3" />
								New workspace
							</DropdownMenuItem>
						</CrumbMenu>
					</>
				)}

				{showEnv && env !== undefined && (
					<>
						<Slash />
						<CrumbMenu
							title="Environment"
							heading="Environments"
							label={env.name}
							icon={<span className={cn("size-[7px] shrink-0 rounded-full", environmentDotClass(env.type))} />}
						>
							{environments.map((e) => (
								<DropdownMenuItem
									key={e.id}
									onClick={() => {
										setEnv(e);
									}}
								>
									<span className={cn("size-[7px] rounded-full", environmentDotClass(e.type))} />
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
