import { Scope } from "@gen/loco/token/v1/token_pb";
import { useQuery } from "@tanstack/react-query";
import {
	Building2Icon,
	ChartLineIcon,
	CornerDownLeftIcon,
	KeyRoundIcon,
	LayersIcon,
	PaletteIcon,
	PlusIcon,
	ScrollTextIcon,
	SearchIcon,
	Settings2Icon,
	UserIcon,
	UserPlusIcon,
	WaypointsIcon,
} from "lucide-react";
import { createContext, use, useEffect, useState, type ReactNode } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router";

import { Button } from "@/components/design/Button";
import {
	Command,
	CommandDialog,
	CommandEmpty,
	CommandFooter,
	CommandGroup,
	CommandInput,
	CommandItem,
	CommandList,
	CommandShortcut,
} from "@/components/design/Command";
import { Kbd } from "@/components/design/Kbd";
import { resourceStatusStyle } from "@/components/design/StatusBadge";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { ModeIcon } from "@/components/design/ThemePreview";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import { scorePaletteItem } from "@/lib/command-palette";
import { environmentStatus } from "@/lib/resource-status";
import {
	accountSettingsPath,
	newResourcePath,
	observabilityPath,
	orgSettingsPath,
	resourcePath,
	switchWorkspacePath,
	type ObservabilityView,
} from "@/lib/routes";
import { colorThemes, modes } from "@/lib/theme";
import { useTheme } from "@/lib/use-theme";

import { useNavSections } from "./nav";

const SHORTCUT_KEY = "k";
const IS_MAC = /Mac|iPhone|iPad/.test(navigator.userAgent);
export const SHORTCUT_LABEL = IS_MAC ? "⌘K" : "Ctrl K";
const PLACEHOLDER = "Search resources, pages and actions";
const PALETTE_GROUPS = ["Actions", "Resources", "Pages", "Workspaces", "Appearance"] as const;
const BROWSE_LIMIT = 8;

interface PaletteItem {
	id: string;
	group: (typeof PALETTE_GROUPS)[number];
	label: string;
	keywords?: string;
	meta?: string | undefined;
	icon: ReactNode;
	run: () => void;
	// Shown only once the person types, so browsing lists each resource once.
	searchOnly?: boolean;
}

const RESOURCE_VIEWS: { view: ObservabilityView; label: string; icon: ReactNode }[] = [
	{ view: "logs", label: "Logs", icon: <ScrollTextIcon /> },
	{ view: "metrics", label: "Metrics", icon: <ChartLineIcon /> },
	{ view: "traces", label: "Traces", icon: <WaypointsIcon /> },
];

function usePaletteItems(open: boolean): PaletteItem[] {
	const navigate = useNavigate();
	const { pathname, search } = useLocation();
	const [params] = useSearchParams();
	const { mode, setMode, color, setColor } = useTheme();
	const { active: activeEnv } = useEnvironments();
	const { activeOrgId, activeWorkspaceId, workspaces } = useOrgWorkspace();
	const navPages = useNavSections().flatMap((section) => section.pages);
	const scopes = useMyScopes();
	const resources = useQuery({
		...useWorkspaceResourcesOptions(activeOrgId ?? "", activeWorkspaceId ?? ""),
		enabled: open && activeOrgId !== null && activeWorkspaceId !== null,
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
				icon: <StatusGlyph glyph={resourceStatusStyle(environmentStatus(r, activeEnv?.id)?.status).glyph} />,
				run: go(resourcePath(orgId, workspaceId, r.id)),
			});
			for (const v of RESOURCE_VIEWS) {
				items.push({
					id: `resource-${r.id}-${v.view}`,
					group: "Resources",
					label: `${v.label} for ${r.name}`,
					keywords: `${v.view} ${r.slug}`,
					icon: v.icon,
					run: go(observabilityPath(orgId, workspaceId, v.view, { ...envParams, r: r.name })),
					searchOnly: true,
				});
			}
		}
	}

	items.push({
		id: "action-new-token",
		group: "Actions",
		label: "New token",
		keywords: "create api ci key",
		icon: <KeyRoundIcon />,
		run: go("/tokens?new=1"),
	});
	if (activeOrgId !== null && scopes.holds(activeOrgId, "", Scope.ADMIN)) {
		items.push({
			id: "action-invite",
			group: "Actions",
			label: "Invite people",
			keywords: "team member email add",
			icon: <UserPlusIcon />,
			run: go(`/org/${activeOrgId}/team?invite=1`),
		});
	}
	for (const page of navPages) {
		if (page.to === undefined) continue;
		const label = page.name === "Settings" ? "Workspace settings" : page.name;
		items.push({
			id: `page-${label.toLowerCase()}`,
			group: "Pages",
			label,
			keywords: page.keywords,
			icon: page.icon,
			run: go(page.to),
		});
	}
	if (activeOrgId !== null) {
		items.push({
			id: "page-organization-settings",
			group: "Pages",
			label: "Organization settings",
			keywords: "org members billing",
			icon: <Settings2Icon />,
			run: go(orgSettingsPath(activeOrgId, activeWorkspaceId)),
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
			id: "page-account",
			group: "Pages",
			label: "Account",
			keywords: "profile personal settings",
			icon: <UserIcon />,
			run: go(accountSettingsPath(activeOrgId, activeWorkspaceId)),
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

	for (const m of modes) {
		items.push({
			id: `mode-${m.id}`,
			group: "Appearance",
			label: `${m.name} mode`,
			keywords: `appearance theme mode${m.id === "system" ? " auto os" : ""}`,
			meta: mode === m.id ? "current" : undefined,
			icon: <ModeIcon mode={m.id} />,
			run: () => {
				setMode(m.id);
			},
		});
	}
	for (const t of colorThemes) {
		items.push({
			id: `color-${t.id}`,
			group: "Appearance",
			label: `${t.name} theme`,
			keywords: "appearance color colour theme",
			meta: color === t.id ? "current" : undefined,
			icon: <PaletteIcon />,
			run: () => {
				setColor(t.id);
			},
		});
	}

	return items;
}

function PaletteBody({ items, onClose }: { items: readonly PaletteItem[]; onClose: () => void }) {
	const [query, setQuery] = useState("");
	const browsing = query.trim() === "";
	const groups = PALETTE_GROUPS.filter((group) => !browsing || group !== "Workspaces").map((group) => {
		const inGroup = items.filter((item) => item.group === group && !(browsing && item.searchOnly === true));
		return { group, items: browsing && group !== "Appearance" ? inGroup.slice(0, BROWSE_LIMIT) : inGroup };
	});

	return (
		<Command label={PLACEHOLDER} filter={scorePaletteItem}>
			<CommandInput value={query} onValueChange={setQuery} placeholder={PLACEHOLDER}>
				<Kbd>esc</Kbd>
			</CommandInput>
			<CommandList label="Results">
				<CommandEmpty>Nothing matches.</CommandEmpty>
				{groups.map(
					(g) =>
						g.items.length > 0 && (
							<CommandGroup key={g.group} heading={g.group}>
								{g.items.map((item) => (
									<CommandItem
										key={item.id}
										value={item.id}
										keywords={[item.label, item.keywords ?? ""]}
										onSelect={() => {
											onClose();
											item.run();
										}}
									>
										<span aria-hidden className="flex w-4 justify-center text-fg3">
											{item.icon}
										</span>
										<span className="truncate">{item.label}</span>
										<CommandShortcut>
											{item.meta !== undefined && item.meta !== "" && <span className="truncate">{item.meta}</span>}
											<CornerDownLeftIcon
												aria-hidden
												className="size-3 shrink-0 text-fg4 opacity-0 group-data-selected/command-item:opacity-100"
											/>
										</CommandShortcut>
									</CommandItem>
								))}
							</CommandGroup>
						),
				)}
			</CommandList>
			<CommandFooter>
				<span className="flex items-center gap-1.5">
					<Kbd>↑</Kbd>
					<Kbd>↓</Kbd>
					to navigate
				</span>
				<span className="flex items-center gap-1.5">
					<Kbd>↵</Kbd>
					to open
				</span>
				<span className="flex items-center gap-1.5">
					<Kbd>esc</Kbd>
					to close
				</span>
			</CommandFooter>
		</Command>
	);
}

const OpenPaletteContext = createContext<(() => void) | null>(null);

export function useOpenPalette(): () => void {
	const openPalette = use(OpenPaletteContext);
	if (openPalette === null) throw new Error("useOpenPalette must be used inside CommandPaletteProvider");
	return openPalette;
}

export function CommandPaletteProvider({ children }: { children: ReactNode }) {
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
		<OpenPaletteContext
			value={() => {
				setOpen(true);
			}}
		>
			{children}
			<CommandDialog open={open} onOpenChange={setOpen} title="Command palette" description={PLACEHOLDER}>
				<PaletteBody
					items={items}
					onClose={() => {
						setOpen(false);
					}}
				/>
			</CommandDialog>
		</OpenPaletteContext>
	);
}

export function CommandMenu() {
	const openPalette = useOpenPalette();

	return (
		<Button
			variant="outline"
			aria-label="Search and run commands"
			aria-keyshortcuts="Meta+K Control+K"
			onClick={openPalette}
			className="px-2 text-fg3 hover:text-fg2 md:pr-2 md:pl-2.5"
		>
			<SearchIcon aria-hidden />
			<span className="hidden md:inline">Search</span>
			<Kbd className="ml-3.5 hidden md:inline-flex">{SHORTCUT_LABEL}</Kbd>
		</Button>
	);
}
