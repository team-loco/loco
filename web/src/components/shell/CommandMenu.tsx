import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import {
	Building2Icon,
	LayersIcon,
	MoonIcon,
	PackageIcon,
	PlusIcon,
	SearchIcon,
	SunIcon,
	UserIcon,
} from "lucide-react";
import { useEffect, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";

import { Button } from "@/components/design/Button";
import { CommandPalette, type PaletteItem } from "@/components/design/CommandPalette";
import { Kbd } from "@/components/design/Kbd";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { newResourcePath, resourcePath, switchWorkspacePath } from "@/lib/routes";
import { useTheme } from "@/lib/use-theme";

import { useNavSections } from "./nav";

const SHORTCUT_KEY = "k";
const IS_MAC = /Mac|iPhone|iPad/.test(navigator.userAgent);
const SHORTCUT_LABEL = IS_MAC ? "⌘K" : "Ctrl K";

function usePaletteItems(open: boolean): PaletteItem[] {
	const navigate = useNavigate();
	const { pathname, search } = useLocation();
	const [params] = useSearchParams();
	const transport = useTransport();
	const { theme, toggle } = useTheme();
	const { activeOrgId, activeWorkspaceId, workspaces } = useOrgWorkspace();
	const navPages = useNavSections().flatMap((section) => section.pages);
	const resources = useQuery({
		...workspaceResourcesOptions(transport, activeWorkspaceId ?? ""),
		enabled: open && activeWorkspaceId !== null,
	});

	const env = params.get("env");
	const envParams: Record<string, string> = env !== null ? { env } : {};
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
			run: go(newResourcePath(orgId, workspaceId, envParams)),
		});
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
	for (const page of navPages) {
		if (page.to === undefined) continue;
		items.push({
			id: `page-${page.name.toLowerCase()}`,
			group: "Pages",
			label: page.name,
			keywords: page.keywords,
			icon: page.icon,
			run: go(page.to),
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
			<Button
				variant="outline"
				aria-label="Search and run commands"
				aria-keyshortcuts="Meta+K Control+K"
				onClick={() => {
					setOpen(true);
				}}
				className="px-2 text-fg3 hover:text-fg2 md:pr-2 md:pl-2.5"
			>
				<SearchIcon aria-hidden />
				<span className="hidden md:inline">Search</span>
				<Kbd className="ml-3.5 hidden md:inline-flex">{SHORTCUT_LABEL}</Kbd>
			</Button>
			<CommandPalette open={open} onOpenChange={setOpen} items={items} />
		</>
	);
}
