import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import {
	BellIcon,
	Building2Icon,
	ChartLineIcon,
	KeyRoundIcon,
	LayersIcon,
	LayoutGridIcon,
	MoonIcon,
	PackageIcon,
	PlusIcon,
	ScrollTextIcon,
	SearchIcon,
	Settings2Icon,
	SunIcon,
	UserIcon,
	UsersIcon,
	WaypointsIcon,
} from "lucide-react";
import { useEffect, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";

import { CommandPalette, type PaletteItem } from "@/components/design/CommandPalette";
import { Kbd } from "@/components/design/Kbd";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import {
	newServicePath,
	observabilityPath,
	resourcePath,
	switchWorkspacePath,
	workspacePath,
	type ObservabilityView,
} from "@/lib/routes";
import { useTheme } from "@/lib/use-theme";

const SHORTCUT_KEY = "k";
const IS_MAC = /Mac|iPhone|iPad/.test(navigator.userAgent);
const SHORTCUT_LABEL = IS_MAC ? "⌘K" : "Ctrl K";

const OBSERVABILITY_PAGES: { view: ObservabilityView; label: string; icon: React.ReactNode }[] = [
	{ view: "logs", label: "Logs", icon: <ScrollTextIcon /> },
	{ view: "metrics", label: "Metrics", icon: <ChartLineIcon /> },
	{ view: "traces", label: "Traces", icon: <WaypointsIcon /> },
	{ view: "events", label: "Events", icon: <BellIcon /> },
];

function usePaletteItems(open: boolean): PaletteItem[] {
	const navigate = useNavigate();
	const { pathname, search } = useLocation();
	const [params] = useSearchParams();
	const transport = useTransport();
	const { theme, toggle } = useTheme();
	const { activeOrgId, activeWorkspaceId, workspaces } = useOrgWorkspace();
	const resources = useQuery({
		...workspaceResourcesOptions(transport, activeWorkspaceId ?? ""),
		enabled: open && activeWorkspaceId !== null,
	});

	const env = params.get("env");
	const envParams: Record<string, string> = env !== null ? { env } : {};
	const withEnv = (path: string) => (env !== null ? `${path}?env=${encodeURIComponent(env)}` : path);
	const go = (to: string) => () => {
		void navigate(to);
	};
	const items: PaletteItem[] = [];

	if (activeOrgId !== null && activeWorkspaceId !== null) {
		const orgId = activeOrgId;
		const workspaceId = activeWorkspaceId;
		items.push({
			id: "action-new-resource",
			group: "Actions",
			label: "New resource",
			keywords: "create deploy service",
			icon: <PlusIcon />,
			run: go(newServicePath(orgId, workspaceId, envParams)),
		});
		items.push({
			id: "page-dashboard",
			group: "Pages",
			label: "Dashboard",
			keywords: "home resources",
			icon: <LayoutGridIcon />,
			run: go(withEnv(workspacePath(orgId, workspaceId))),
		});
		for (const page of OBSERVABILITY_PAGES) {
			items.push({
				id: `page-${page.view}`,
				group: "Pages",
				label: page.label,
				keywords: "observability",
				icon: page.icon,
				run: go(observabilityPath(orgId, workspaceId, page.view, envParams)),
			});
		}
		for (const r of resources.data?.resources ?? []) {
			items.push({
				id: `resource-${r.id}`,
				group: "Resources",
				label: r.name,
				keywords: `${r.slug} ${r.description ?? ""}`,
				meta: r.description,
				icon: <PackageIcon />,
				run: go(resourcePath(orgId, workspaceId, r.id)),
			});
		}
	}

	items.push({
		id: "action-theme",
		group: "Actions",
		label: theme === "dark" ? "Switch to light mode" : "Switch to dark mode",
		keywords: "theme appearance toggle",
		icon: theme === "dark" ? <SunIcon /> : <MoonIcon />,
		run: toggle,
	});
	items.push({
		id: "page-tokens",
		group: "Pages",
		label: "Tokens",
		keywords: "api keys access",
		icon: <KeyRoundIcon />,
		run: go("/tokens"),
	});
	if (activeOrgId !== null) {
		items.push({
			id: "page-team",
			group: "Pages",
			label: "Team",
			keywords: "members people",
			icon: <UsersIcon />,
			run: go(`/org/${activeOrgId}/team`),
		});
	}
	if (activeOrgId !== null && activeWorkspaceId !== null) {
		items.push({
			id: "page-settings",
			group: "Pages",
			label: "Settings",
			keywords: "workspace environments",
			icon: <Settings2Icon />,
			run: go(withEnv(workspacePath(activeOrgId, activeWorkspaceId, "settings"))),
		});
	}
	items.push(
		{
			id: "page-organizations",
			group: "Pages",
			label: "Organizations",
			keywords: "orgs",
			icon: <Building2Icon />,
			run: go("/organizations"),
		},
		{
			id: "page-profile",
			group: "Pages",
			label: "Profile",
			keywords: "account",
			icon: <UserIcon />,
			run: go("/profile"),
		},
	);

	for (const w of workspaces) {
		if (w.id === activeWorkspaceId) continue;
		items.push({
			id: `workspace-${w.id}`,
			group: "Workspaces",
			label: `Switch to ${w.name}`,
			keywords: `workspace ${w.orgName}`,
			meta: w.orgName,
			icon: <LayersIcon />,
			run: go(switchWorkspacePath(pathname, search, w.orgId, w.id)),
		});
	}

	return items;
}

export function CommandMenu() {
	const [open, setOpen] = useState(false);
	const items = usePaletteItems(open);

	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key.toLowerCase() !== SHORTCUT_KEY || !(event.metaKey || event.ctrlKey)) return;
			event.preventDefault();
			setOpen((was) => !was);
		};
		window.addEventListener("keydown", onKeyDown);
		return () => {
			window.removeEventListener("keydown", onKeyDown);
		};
	}, []);

	return (
		<>
			<button
				type="button"
				aria-label="Search and run commands"
				aria-keyshortcuts="Meta+K Control+K"
				onClick={() => {
					setOpen(true);
				}}
				className="flex h-8 shrink-0 items-center gap-2 rounded-md border border-line bg-raise px-2 text-[13px] text-fg3 outline-none transition-colors duration-150 hover:border-line2 hover:text-fg2 focus-visible:ring-2 focus-visible:ring-ring/50 md:pr-2 md:pl-2.5"
			>
				<SearchIcon aria-hidden className="size-3.5" />
				<span className="hidden md:inline">Search</span>
				<Kbd className="ml-3.5 hidden md:inline-flex">{SHORTCUT_LABEL}</Kbd>
			</button>
			<CommandPalette open={open} onOpenChange={setOpen} items={items} />
		</>
	);
}
