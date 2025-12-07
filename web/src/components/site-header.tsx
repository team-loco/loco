import { SearchForm } from "@/components/search-form";
import { useSidebar } from "@/components/ui/sidebar";
import { listApps } from "@/gen/app/v1";
import { getCurrentUserOrgs } from "@/gen/org/v1";
import { listWorkspaces } from "@/gen/workspace/v1";
import { useQuery } from "@connectrpc/connect-query";
import { PanelLeftIcon, PanelLeftCloseIcon } from "lucide-react";
import { Link, useLocation, useSearchParams } from "react-router";
import { Button } from "@/components/ui/button";

export function SiteHeader() {
	const location = useLocation();
	const [searchParams] = useSearchParams();
	const { open, toggleSidebar } = useSidebar();

	const workspaceFromUrl = searchParams.get("workspace");
	const activeWorkspaceId = workspaceFromUrl ? BigInt(workspaceFromUrl) : null;

	const appIdMatch = location.pathname.match(/\/app\/(\d+)/);
	const activeAppId = appIdMatch ? BigInt(appIdMatch[1]) : null;

	const { data: orgsRes } = useQuery(getCurrentUserOrgs, {});
	const firstOrgId = orgsRes?.orgs?.[0]?.id ?? null;

	const { data: workspacesRes } = useQuery(
		listWorkspaces,
		firstOrgId ? { orgId: firstOrgId } : undefined,
		{ enabled: !!firstOrgId }
	);

	const { data: appsRes } = useQuery(
		listApps,
		{ workspaceId: activeWorkspaceId ?? 0n },
		{ enabled: !!activeWorkspaceId }
	);

	const isHome = !activeAppId && !activeWorkspaceId;
	const workspaceName = workspacesRes?.workspaces?.find(
		(ws) => ws.id === activeWorkspaceId
	)?.name;
	const appName = appsRes?.apps?.find((app) => app.id === activeAppId)?.name;

	return (
		<header 
			className="bg-white dark:bg-[oklch(0.2553_0.0226_262.4337)] fixed top-0 left-0 right-0 z-40 flex w-full items-center border-b border-neutral-300 dark:border-neutral-700 dark:text-white"
			style={{ "--header-height": "70px" } as React.CSSProperties}
		>
			<div className="flex h-14 w-full items-center gap-3 px-6">
				<Button
					variant="ghost"
					size="icon"
					onClick={toggleSidebar}
					className={`h-8 w-8 shrink-0 transition-colors ${
						open ? "bg-accent text-accent-foreground" : ""
					}`}
					aria-label="Toggle Sidebar"
				>
					{open ? (
						<PanelLeftCloseIcon className="h-4 w-4" />
					) : (
						<PanelLeftIcon className="h-4 w-4" />
					)}
				</Button>
				<nav className="hidden sm:flex items-center gap-3 text-sm font-mono">
					<Link
						to="/dashboard"
						className="hover:text-foreground text-muted-foreground transition-colors"
					>
						Home
					</Link>
					{!isHome && workspaceName && activeWorkspaceId && (
						<>
							<span className="text-muted-foreground">/</span>
							<Link
								to={`/dashboard?workspace=${activeWorkspaceId}`}
								className="hover:text-foreground text-foreground transition-colors"
							>
								{workspaceName}
							</Link>
						</>
					)}
					{!isHome && appName && activeAppId && activeWorkspaceId && (
						<>
							<span className="text-muted-foreground">/</span>
							<span className="text-foreground">{appName}</span>
						</>
					)}
				</nav>
				<SearchForm className="w-full sm:ml-auto sm:w-auto" />
			</div>
		</header>
	);
}
