import { useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { Building2Icon, CheckIcon, ChevronDownIcon, ChevronsUpDownIcon, PlusIcon, Settings2Icon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, matchPath, useLocation, useNavigate } from "react-router";

import type { Organization } from "@gen/loco/org/v1/org_pb";
import { Scope } from "@gen/loco/token/v1/token_pb";
import { getWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";

import { Breadcrumb, BreadcrumbItem, BreadcrumbList, BreadcrumbSeparator } from "@/components/design/Breadcrumb";
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
import { orgWorkspacesOptions } from "@/hooks/orgWorkspaces";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useMyScopes } from "@/hooks/useMyScopes";
import { orgSettingsPath, settingsPath, switchWorkspacePath, WORKSPACE_ROUTE, workspacePath } from "@/lib/routes";
import { cn } from "@/lib/utils";

import { CreateScopeDialog, type ScopeKind } from "./CreateScopeDialog";

const TRIGGER =
	"flex h-[26px] min-w-0 items-center gap-[5px] rounded-[5px] px-1.5 -mx-0.5 font-mono text-[12.5px] whitespace-nowrap text-foreground outline-none transition-colors duration-150 ease-out hover:bg-panel focus-visible:ring-2 focus-visible:ring-ring/50 aria-expanded:bg-panel";

function Slash() {
	return <BreadcrumbSeparator className="px-0.5 text-fg3">/</BreadcrumbSeparator>;
}

function EnvSwitcher({ children, label }: { children: ReactNode; label: string }) {
	return (
		<BreadcrumbItem className="min-w-0">
			<DropdownMenu>
				<DropdownMenuTrigger title="Switch environment" className={cn(TRIGGER, "max-w-[220px]")}>
					<span className="truncate">{label}</span>
					<ChevronDownIcon className="size-3.5 shrink-0 text-fg3" />
				</DropdownMenuTrigger>
				<DropdownMenuContent className="w-[240px]" align="start" sideOffset={6} alignOffset={-4}>
					<DropdownMenuGroup>
						<DropdownMenuLabel>Environments</DropdownMenuLabel>
						{children}
					</DropdownMenuGroup>
				</DropdownMenuContent>
			</DropdownMenu>
		</BreadcrumbItem>
	);
}

function OrgSwitcher({ org, onCreate }: { org: Organization; onCreate: () => void }) {
	const navigate = useNavigate();
	const transport = useTransport();
	const { orgs, activeWorkspaceId } = useOrgWorkspace();
	const results = useQueries({
		queries: orgs.map((o) => orgWorkspacesOptions(transport, o.id)),
	});

	return (
		<BreadcrumbItem className="min-w-0">
			<DropdownMenu>
				<DropdownMenuTrigger title="Switch organization" className={cn(TRIGGER, "max-w-[220px]")}>
					<span className="truncate text-fg3">{org.name}</span>
					<ChevronsUpDownIcon className="size-3.5 shrink-0 text-fg3" />
				</DropdownMenuTrigger>
				<DropdownMenuContent className="w-[248px]" align="start" sideOffset={6} alignOffset={-4}>
					<DropdownMenuGroup>
						<DropdownMenuLabel>Organizations</DropdownMenuLabel>
						{orgs.map((o, i) => {
							const first = results[i]?.data?.workspaces[0];
							return (
								<DropdownMenuItem
									key={o.id}
									onClick={() => {
										if (o.id === org.id) return;
										void navigate(first === undefined ? `/org/${o.id}/team` : workspacePath(o.id, first.id));
									}}
								>
									<span className="flex-1 truncate">{o.name}</span>
									{o.id === org.id && <CheckIcon className="size-3.5" />}
								</DropdownMenuItem>
							);
						})}
					</DropdownMenuGroup>
					<DropdownMenuSeparator />
					<DropdownMenuItem
						onClick={() => {
							void navigate(orgSettingsPath(org.id, activeWorkspaceId));
						}}
					>
						<Settings2Icon className="text-fg3" />
						Organization settings
					</DropdownMenuItem>
					<DropdownMenuItem onClick={onCreate}>
						<PlusIcon className="text-fg3" />
						New organization
					</DropdownMenuItem>
					<DropdownMenuItem
						onClick={() => {
							void navigate("/organizations");
						}}
					>
						<Building2Icon className="text-fg3" />
						All organizations
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
		</BreadcrumbItem>
	);
}

function WorkspaceSwitcher({
	org,
	workspace,
	onCreate,
}: {
	org: Organization;
	workspace: Workspace;
	onCreate: () => void;
}) {
	const { pathname, search } = useLocation();
	const navigate = useNavigate();
	const { workspaces, activeWorkspaceId } = useOrgWorkspace();
	const { orgLevel } = useMyScopes();

	return (
		<BreadcrumbItem className="min-w-0">
			<DropdownMenu>
				<DropdownMenuTrigger title="Switch workspace" className={cn(TRIGGER, "max-w-[220px]")}>
					<span className="truncate">{workspace.name}</span>
					<ChevronsUpDownIcon className="size-3.5 shrink-0 text-fg3" />
				</DropdownMenuTrigger>
				<DropdownMenuContent className="w-[248px]" align="start" sideOffset={6} alignOffset={-4}>
					<DropdownMenuGroup>
						<DropdownMenuLabel>Workspaces</DropdownMenuLabel>
						{workspaces
							.filter((w) => w.orgId === org.id)
							.map((w) => (
								<DropdownMenuItem
									key={w.id}
									onClick={() => {
										if (w.id !== activeWorkspaceId) void navigate(switchWorkspacePath(pathname, search, org.id, w.id));
									}}
								>
									<span className="flex-1 truncate">{w.name}</span>
									{w.id === activeWorkspaceId && <CheckIcon className="size-3.5" />}
								</DropdownMenuItem>
							))}
					</DropdownMenuGroup>
					{orgLevel(org.id) >= 2 && (
						<>
							<DropdownMenuSeparator />
							<DropdownMenuItem onClick={onCreate}>
								<PlusIcon className="text-fg3" />
								New workspace
							</DropdownMenuItem>
						</>
					)}
				</DropdownMenuContent>
			</DropdownMenu>
		</BreadcrumbItem>
	);
}

export function ShellBreadcrumb() {
	const { pathname } = useLocation();
	const { orgs, workspaces, activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { environments, active: env, setActive: setEnv } = useEnvironments();
	const [creating, setCreating] = useState<ScopeKind | null>(null);

	const org = orgs.find((o) => o.id === activeOrgId);
	const scopes = useMyScopes();
	const directWorkspace = useQuery(
		getWorkspace,
		{ workspaceId: activeWorkspaceId ?? "" },
		{
			enabled:
				org === undefined &&
				activeOrgId !== null &&
				activeWorkspaceId !== null &&
				!scopes.isLoading &&
				scopes.holds(activeOrgId, activeWorkspaceId, Scope.READ),
		},
	);
	const workspace = workspaces.find((w) => w.id === activeWorkspaceId) ?? directWorkspace.data?.workspace;
	const hasScopeCrumb = org !== undefined || workspace !== undefined;
	const inWorkspace = matchPath(WORKSPACE_ROUTE, pathname) !== null;
	const showEnv = inWorkspace && !pathname.endsWith("/settings");

	return (
		<Breadcrumb className="min-w-0 flex-1 overflow-hidden">
			<BreadcrumbList>
				{org !== undefined && (
					<>
						<OrgSwitcher
							org={org}
							onCreate={() => {
								setCreating("org");
							}}
						/>
						{workspace !== undefined && (
							<>
								<Slash />
								<WorkspaceSwitcher
									org={org}
									workspace={workspace}
									onCreate={() => {
										setCreating("workspace");
									}}
								/>
							</>
						)}
					</>
				)}

				{org === undefined && workspace !== undefined && (
					<BreadcrumbItem className="min-w-0">
						<span className="truncate px-2 font-mono text-[12.5px] text-fg3">
							{workspace.orgName} / {workspace.name}
						</span>
					</BreadcrumbItem>
				)}

				{showEnv && env !== undefined && (
					<>
						{hasScopeCrumb && <Slash />}
						<EnvSwitcher label={env.name}>
							{environments.map((e) => (
								<DropdownMenuItem
									key={e.id}
									onClick={() => {
										setEnv(e);
									}}
								>
									<span className="flex-1 truncate font-mono text-[12.5px]">{e.name}</span>
									<CheckIcon className={cn("size-3.5", e.id !== env.id && "invisible")} />
								</DropdownMenuItem>
							))}
							{activeOrgId !== null && activeWorkspaceId !== null && (
								<>
									<DropdownMenuSeparator />
									<DropdownMenuItem
										render={<Link to={settingsPath(activeOrgId, activeWorkspaceId, "workspace")} />}
										className="text-[13px] text-fg2"
									>
										<Settings2Icon className="text-fg3" />
										Manage environments
									</DropdownMenuItem>
								</>
							)}
						</EnvSwitcher>
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
