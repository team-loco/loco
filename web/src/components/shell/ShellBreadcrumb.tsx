import { useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { Building2Icon, CheckIcon, ChevronDownIcon, ChevronsUpDownIcon, PlusIcon, SettingsIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Link, matchPath, useLocation, useNavigate } from "react-router";

import { EnvironmentType } from "@gen/loco/environment/v1/environment_pb";
import type { Organization } from "@gen/loco/org/v1/org_pb";
import { Scope } from "@gen/loco/token/v1/token_pb";
import { getWorkspace } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";

import { Breadcrumb, BreadcrumbItem, BreadcrumbList, BreadcrumbSeparator } from "@/components/design/Breadcrumb";
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
import { orgWorkspacesOptions } from "@/hooks/orgWorkspaces";
import { environmentTypeLabel, useEnvironments } from "@/hooks/useEnvironment";
import { useMyScopes } from "@/hooks/useMyScopes";
import { switchWorkspacePath, WORKSPACE_ROUTE, workspacePath } from "@/lib/routes";
import { cn } from "@/lib/utils";

import { CreateScopeDialog, type ScopeKind } from "./CreateScopeDialog";

const TRIGGER =
	"flex h-8 min-w-0 items-center gap-2 rounded-md px-2 font-mono text-[12.5px] whitespace-nowrap text-foreground outline-none transition-colors duration-150 ease-out hover:bg-panel focus-visible:ring-2 focus-visible:ring-ring/50 aria-expanded:bg-panel";

function Slash() {
	return <BreadcrumbSeparator className="px-0.5 text-fg4">/</BreadcrumbSeparator>;
}

function envDot(type: EnvironmentType): string {
	switch (type) {
		case EnvironmentType.PRODUCTION:
			return "bg-info";
		case EnvironmentType.STAGING:
			return "bg-warn";
		case EnvironmentType.UNSPECIFIED:
			return "bg-fg4";
	}
}

function EnvSwitcher({ children, label }: { children: ReactNode; label: string }) {
	return (
		<BreadcrumbItem className="min-w-0">
			<DropdownMenu>
				<DropdownMenuTrigger title="Switch environment" className={cn(TRIGGER, "max-w-[220px]")}>
					<span className="truncate">{label}</span>
					<ChevronDownIcon className="size-3.5 shrink-0 text-fg3" />
				</DropdownMenuTrigger>
				<DropdownMenuContent className="w-[240px]" align="start">
					<DropdownMenuGroup>
						<DropdownMenuLabel>Environments</DropdownMenuLabel>
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
	const transport = useTransport();
	const { orgs, activeWorkspaceId } = useOrgWorkspace();
	const { orgLevel } = useMyScopes();
	const [open, setOpen] = useState(false);
	const results = useQueries({
		queries: orgs.map((o) => orgWorkspacesOptions(transport, o.id)),
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
				}}
			>
				<DropdownMenuTrigger title="Switch workspace" className={cn(TRIGGER, "max-w-[360px]")}>
					<span className="truncate text-fg3">{org.name}</span>
					{workspace !== undefined && (
						<>
							<span className="text-fg4">/</span>
							<span className="truncate">{workspace.name}</span>
						</>
					)}
					<ChevronsUpDownIcon className="size-3.5 shrink-0 text-fg3" />
				</DropdownMenuTrigger>
				<DropdownMenuContent className="w-[248px]" align="start">
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
										onClick={() => {
											if (w.id !== activeWorkspaceId) void navigate(switchWorkspacePath(pathname, search, o.id, w.id));
										}}
									>
										<span className="flex-1 truncate">{w.name}</span>
										{w.id === activeWorkspaceId && <CheckIcon className="size-3.5" />}
									</DropdownMenuItem>
								))}
							</DropdownMenuGroup>
						);
					})}
					<DropdownMenuSeparator />
					{orgLevel(org.id) >= 2 && (
						<DropdownMenuItem
							onClick={() => {
								onCreate("workspace");
							}}
						>
							<PlusIcon className="text-fg3" />
							New workspace
						</DropdownMenuItem>
					)}
					<DropdownMenuItem
						onClick={() => {
							onCreate("org");
						}}
					>
						<PlusIcon className="text-fg3" />
						New organization
					</DropdownMenuItem>
					<DropdownMenuItem
						onClick={() => {
							go("/organizations");
						}}
					>
						<Building2Icon className="text-fg3" />
						Organizations
					</DropdownMenuItem>
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
					<ScopeSwitcher
						org={org}
						workspace={workspace}
						onCreate={(kind) => {
							setCreating(kind);
						}}
					/>
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
									<span aria-hidden className={cn("size-[7px] shrink-0 rounded-full", envDot(e.type))} />
									<span className="flex-1 truncate font-mono text-[12.5px]">{e.name}</span>
									<span className="font-mono text-[10.5px] text-fg3">{environmentTypeLabel(e.type)}</span>
									<CheckIcon className={cn("size-3.5", e.id !== env.id && "invisible")} />
								</DropdownMenuItem>
							))}
							{activeOrgId !== null && activeWorkspaceId !== null && (
								<>
									<DropdownMenuSeparator />
									<DropdownMenuItem
										render={<Link to={workspacePath(activeOrgId, activeWorkspaceId, "settings")} />}
										className="text-[13px] text-fg2"
									>
										<SettingsIcon className="text-fg3" />
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
