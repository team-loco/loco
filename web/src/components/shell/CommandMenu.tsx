import { useQueryState } from "nuqs";
import { useInSystem } from "@/hooks/useInSystem";
import { Scope } from "@gen/loco/token/v1/token_pb";
import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import {
	ArrowRightIcon,
	Building2Icon,
	ChevronRightIcon,
	CopyIcon,
	KeyboardIcon,
	SunMoonIcon,
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
	WebhookIcon,
} from "lucide-react";
import { createContext, use, useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useLocation, useNavigate } from "react-router";

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
import { useCopy } from "@/hooks/useCopy";
import { useEnvironments } from "@/hooks/useEnvironment";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { useWorkspace } from "@/hooks/useWorkspace";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import { scorePaletteItem } from "@/lib/command-palette";
import { environmentStatus } from "@/lib/resource-status";
import {
	accountSettingsPath,
	webhooksPath,
	newResourcePath,
	observabilityPath,
	orgSettingsPath,
	resourcePath,
	switchWorkspacePath,
	type ObservabilityView,
} from "@/lib/routes";
import { shortcutLabel } from "@/lib/keyboard";
import { colorThemes, modes } from "@/lib/theme";
import { useTheme } from "@/lib/use-theme";
import { cn, formatShortId } from "@/lib/utils";

import { useOpenShortcuts } from "./KeyboardShortcuts";

import { useNavSections } from "./nav";

const SHORTCUT_KEY = "k";
export const SHORTCUT_LABEL = shortcutLabel("K");
const PLACEHOLDER = "Search resources, pages and actions";
const PALETTE_GROUPS = [
	"This page",
	"Actions",
	"Resources",
	"Copy",
	"Go to",
	"Workspaces",
	"Appearance",
	"Help",
] as const;
const SEARCH_GROUPS = [
	"This page",
	"Actions",
	"Go to",
	"Resources",
	"Copy",
	"Workspaces",
	"Appearance",
	"Help",
] as const;
const KEY_HINT = "bg-panel2 text-fg3 inset-ring-0";
const BROWSE_LIMIT = 8;
const SEARCH_LIMIT = 6;
// Searching lists this many of the newest workspaces and this many that match the typed text.
const PALETTE_WORKSPACES = 20;

// A list of commands that replaces the main one, such as the modes or one resource's commands.
interface SubList {
	id: string;
	crumb: string;
	heading: string;
	placeholder: string;
}

interface PaletteItem {
	id: string;
	group: (typeof PALETTE_GROUPS)[number];
	label: string;
	keywords?: string;
	meta?: ReactNode;
	icon: ReactNode;
	run: (() => void) | SubList;
	list?: string;
	// Shown only once the person types, so browsing lists each resource once.
	searchOnly?: boolean;
}

export type PageCommand = Omit<PaletteItem, "group" | "list" | "searchOnly">;

const MODE_LIST: SubList = {
	id: "mode",
	crumb: "Mode",
	heading: "Mode",
	placeholder: "Choose a mode",
};
const THEME_LIST: SubList = {
	id: "theme",
	crumb: "Theme",
	heading: "Theme",
	placeholder: "Choose a theme",
};

const RESOURCE_VIEWS: {
	view: ObservabilityView;
	label: string;
	icon: ReactNode;
}[] = [
	{ view: "logs", label: "Logs", icon: <ScrollTextIcon /> },
	{ view: "metrics", label: "Metrics", icon: <ChartLineIcon /> },
	{ view: "traces", label: "Traces", icon: <WaypointsIcon /> },
];

function usePaletteItems(open: boolean, pageItems: readonly PaletteItem[]): PaletteItem[] {
	const navigate = useNavigate();
	const [, copy] = useCopy();
	const openShortcuts = useOpenShortcuts();
	const { pathname } = useLocation();
	const [env] = useQueryState("env");
	const { mode, setMode, color, setColor } = useTheme();
	const { active: activeEnv } = useEnvironments();
	const { activeOrgId, activeWorkspaceId, orgs } = useOrgWorkspace();
	const { workspace } = useWorkspace(activeOrgId, activeWorkspaceId);
	const navPages = useNavSections().flatMap((section) => section.pages);
	const scopes = useMyScopes();
	const resources = useQuery({
		...useWorkspaceResourcesOptions(activeOrgId ?? "", activeWorkspaceId ?? ""),
		enabled: open && activeOrgId !== null && activeWorkspaceId !== null,
	});

	const envParams: Record<string, string> = env !== null ? { env } : {};
	const go = (to: string) => () => {
		void navigate(to);
	};
	const copyItem = (what: string, id: string, keywords: string) => ({
		label: `Copy ${what}`,
		keywords: `id uuid ${keywords}`,
		meta: <span className="font-mono">{formatShortId(id)}</span>,
		icon: <CopyIcon />,
		run: () => {
			copy(id, id, `Copied ${what}`, id);
		},
	});
	const items: PaletteItem[] = [...pageItems];

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
		const resourceList = resources.data?.resources ?? [];
		const here = resourceList.find((r) => pathname === resourcePath(orgId, workspaceId, r.id));
		const copyEnv =
			activeEnv === undefined
				? undefined
				: copyItem(`${activeEnv.name} environment ID`, activeEnv.id, "environment env");
		if (here !== undefined)
			items.push({
				id: "copy-resource-id",
				group: "Copy",
				...copyItem(`${here.name} resource ID`, here.id, `resource service ${here.slug}`),
			});
		if (copyEnv !== undefined) items.push({ id: "copy-environment-id", group: "Copy", ...copyEnv });
		const workspaceName = workspace?.name;
		const orgName = orgs.find((o) => o.id === orgId)?.name;
		items.push(
			{
				id: "copy-workspace-id",
				group: "Copy",
				...copyItem(
					workspaceName === undefined ? "workspace ID" : `${workspaceName} workspace ID`,
					workspaceId,
					"workspace project",
				),
			},
			{
				id: "copy-organization-id",
				group: "Copy",
				...copyItem(
					orgName === undefined ? "organization ID" : `${orgName} organization ID`,
					orgId,
					"organization org",
				),
			},
		);
		for (const r of resourceList) {
			const list: SubList = {
				id: `resource:${r.id}`,
				crumb: r.name,
				heading: "Resource",
				placeholder: `What do you want to do with ${r.name}?`,
			};
			items.push({
				id: `resource-${r.id}`,
				group: "Resources",
				label: r.name,
				keywords: `${r.slug} ${r.description ?? ""}`,
				meta: r.description,
				icon: <StatusGlyph glyph={resourceStatusStyle(environmentStatus(r, activeEnv?.id)?.status).glyph} />,
				run: list,
			});
			for (const v of RESOURCE_VIEWS) {
				items.push({
					id: `resource-${r.id}-${v.view}`,
					group: "Resources",
					label: `${v.label} for ${r.name}`,
					keywords: `${v.view} ${r.slug}`,
					icon: v.icon,
					run: go(
						observabilityPath(orgId, workspaceId, v.view, {
							...envParams,
							r: [r.slug],
						}),
					),
					searchOnly: true,
				});
			}
			if (r.id !== here?.id)
				items.push({
					id: `resource-${r.id}-copy`,
					group: "Resources",
					...copyItem(`${r.name} resource ID`, r.id, r.slug),
					searchOnly: true,
				});
			const sub = (key: string, item: Omit<PaletteItem, "id" | "group" | "list">) => {
				items.push({
					id: `resource-${r.id}-sub-${key}`,
					group: "Resources",
					list: list.id,
					...item,
				});
			};
			sub("open", {
				label: `Open ${r.name}`,
				keywords: "overview",
				icon: <ArrowRightIcon />,
				run: go(resourcePath(orgId, workspaceId, r.id, { env })),
			});
			for (const v of RESOURCE_VIEWS)
				sub(v.view, {
					label: v.label,
					icon: v.icon,
					run: go(
						observabilityPath(orgId, workspaceId, v.view, {
							...envParams,
							r: [r.slug],
						}),
					),
				});
			sub("settings", {
				label: "Settings",
				keywords: "variables env scaling",
				icon: <Settings2Icon />,
				run: go(resourcePath(orgId, workspaceId, r.id, { tab: "settings", env })),
			});
			sub("copy-id", {
				...copyItem(`${r.name} resource ID`, r.id, ""),
				label: "Copy resource ID",
			});
			if (copyEnv !== undefined) sub("copy-environment-id", copyEnv);
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
	if (
		activeOrgId !== null &&
		activeWorkspaceId !== null &&
		scopes.workspaceLevel(activeOrgId, activeWorkspaceId) >= scopeLevel(Scope.ADMIN)
	)
		items.push({
			id: "action-new-webhook",
			group: "Actions",
			label: "New webhook",
			keywords: "create endpoint subscription",
			icon: <WebhookIcon />,
			run: go(webhooksPath(activeOrgId, activeWorkspaceId, "")),
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
	items.push({
		id: "action-new-organization",
		group: "Actions",
		label: "New organization",
		keywords: "create org",
		icon: <Building2Icon />,
		run: go("/organizations?new=1"),
	});
	for (const page of navPages) {
		if (page.to === undefined) continue;
		const label = page.name === "Settings" ? "Workspace settings" : page.name;
		items.push({
			id: `page-${label.toLowerCase()}`,
			group: "Go to",
			label,
			keywords: page.keywords,
			icon: page.icon,
			run: go(page.to),
		});
	}
	if (activeOrgId !== null) {
		items.push({
			id: "page-organization-settings",
			group: "Go to",
			label: "Organization settings",
			keywords: "org members billing",
			icon: <Settings2Icon />,
			run: go(orgSettingsPath(activeOrgId, activeWorkspaceId)),
		});
	}
	items.push(
		{
			id: "page-organizations",
			group: "Go to",
			label: "Organizations",
			keywords: "orgs",
			icon: <Building2Icon />,
			run: go("/organizations"),
		},
		{
			id: "page-account",
			group: "Go to",
			label: "Account",
			keywords: "profile personal settings",
			icon: <UserIcon />,
			run: go(accountSettingsPath(activeOrgId, activeWorkspaceId)),
		},
	);

	items.push(
		{
			id: "set-mode",
			group: "Appearance",
			label: "Set mode…",
			keywords: "appearance dark light system",
			meta: modes.find((item) => item.id === mode)?.name,
			icon: <SunMoonIcon />,
			run: MODE_LIST,
		},
		{
			id: "set-theme",
			group: "Appearance",
			label: "Set theme…",
			keywords: "appearance color colour",
			meta: colorThemes.find((item) => item.id === color)?.name,
			icon: <PaletteIcon />,
			run: THEME_LIST,
		},
		{
			id: "keyboard-shortcuts",
			group: "Help",
			label: "Keyboard shortcuts",
			keywords: "keys hotkeys help",
			meta: "?",
			icon: <KeyboardIcon />,
			run: openShortcuts,
		},
	);

	for (const m of modes) {
		items.push({
			id: `mode-${m.id}`,
			list: "mode",
			group: "Appearance",
			label: m.name,
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
			list: "theme",
			group: "Appearance",
			label: t.name,
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

// The newest workspaces of the active organization, and those whose name or description holds the typed text.
function useWorkspaceItems(query: string): PaletteItem[] {
	const navigate = useNavigate();
	const { pathname, search } = useLocation();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const scopes = useMyScopes();
	const orgId = activeOrgId ?? "";
	const text = query.trim();
	const enabled = text !== "" && activeOrgId !== null && !scopes.isLoading && scopes.orgMember(activeOrgId);
	const newest = useConnectQuery(listOrgWorkspaces, { orgId, pageSize: PALETTE_WORKSPACES }, { enabled });
	const matching = useConnectQuery(
		listOrgWorkspaces,
		{ orgId, pageSize: PALETTE_WORKSPACES, query: text },
		{ enabled, placeholderData: keepPreviousData },
	);
	const workspaces = new Map(
		[...(newest.data?.workspaces ?? []), ...(matching.data?.workspaces ?? [])].map((w) => [w.id, w]),
	);
	return [...workspaces.values()]
		.filter((w) => w.id !== activeWorkspaceId)
		.map((w) => ({
			id: `workspace-${w.id}`,
			group: "Workspaces",
			label: `Switch to ${w.name}`,
			keywords: `workspace ${w.orgName} ${w.description}`,
			meta: w.orgName,
			icon: <LayersIcon />,
			run: () => {
				void navigate(switchWorkspacePath(pathname, search, w.orgId, w.id));
			},
		}));
}

function PaletteBody({ items: commands, onClose }: { items: readonly PaletteItem[]; onClose: () => void }) {
	const [query, setQuery] = useState("");
	const workspaceItems = useWorkspaceItems(query);
	const items = [...commands, ...workspaceItems];
	const [selected, setSelected] = useState("");
	const [list, setList] = useState<SubList | null>(null);
	const inputRef = useRef<HTMLInputElement>(null);
	const placeholder = list?.placeholder ?? PLACEHOLDER;
	const back = () => {
		setList(null);
		setSelected("");
		setQuery("");
		inputRef.current?.focus();
	};
	const browsing = query.trim() === "";
	const groups = (browsing ? PALETTE_GROUPS : SEARCH_GROUPS)
		.filter((group) => !browsing || group !== "Workspaces")
		.map((group) => {
			const inGroup = items.filter(
				(item) =>
					item.group === group && (item.list ?? null) === (list?.id ?? null) && !(browsing && item.searchOnly === true),
			);
			if (browsing)
				return {
					group,
					items: group !== "Appearance" ? inGroup.slice(0, BROWSE_LIMIT) : inGroup,
				};
			if (list !== null) return { group, items: inGroup };
			const scored = inGroup.map((item) => ({
				item,
				score: scorePaletteItem(item.id, query, [item.label, item.keywords ?? ""]),
			}));
			const best = scored.filter((entry) => entry.score > 0).toSorted((a, b) => b.score - a.score);
			return {
				group,
				items: best.slice(0, SEARCH_LIMIT).map((entry) => entry.item),
			};
		});

	return (
		<Command
			value={selected}
			onValueChange={setSelected}
			label={placeholder}
			filter={scorePaletteItem}
			onKeyDownCapture={(event) => {
				if (list !== null && (event.key === "Escape" || (event.key === "Backspace" && query === ""))) {
					event.preventDefault();
					event.stopPropagation();
					back();
				}
			}}
		>
			<CommandInput
				ref={inputRef}
				value={query}
				onValueChange={setQuery}
				placeholder={placeholder}
				leading={
					list !== null ? (
						<Button
							variant="ghost"
							size="xs"
							className="cmd-crumb h-6 shrink-0 bg-panel2 px-2 text-[12.5px] text-foreground hover:bg-line"
							aria-label="Back to all commands"
							onClick={back}
						>
							{list.crumb}
						</Button>
					) : undefined
				}
			>
				<Kbd className={cn(KEY_HINT, "px-1.5 py-0.5 text-[11px]")}>esc</Kbd>
			</CommandInput>
			<CommandList label="Results">
				<CommandEmpty>
					<span className="block text-foreground">Nothing matches.</span>
					<span className="block">Try a page, resource or action, such as Logs, Settings or New resource.</span>
				</CommandEmpty>
				{groups.map(
					(g) =>
						g.items.length > 0 && (
							<CommandGroup key={g.group} heading={list?.heading ?? g.group}>
								{g.items.map((item) => (
									<CommandItem
										key={item.id}
										value={item.id}
										keywords={[item.label, item.keywords ?? ""]}
										onSelect={() => {
											if (typeof item.run !== "function") {
												const next = item.run;
												setList(next);
												setSelected(items.find((option) => option.list === next.id)?.id ?? "");
												setQuery("");
												inputRef.current?.focus();
											} else {
												onClose();
												item.run();
											}
										}}
									>
										<span aria-hidden className="flex w-4 justify-center text-fg3">
											{item.icon}
										</span>
										<span className="truncate">{item.label}</span>
										<CommandShortcut>
											{item.meta !== undefined && item.meta !== "" && <span className="truncate">{item.meta}</span>}
											{typeof item.run === "function" ? (
												<CornerDownLeftIcon
													aria-hidden
													className="size-3 shrink-0 text-fg4 opacity-0 group-data-selected/command-item:opacity-100"
												/>
											) : (
												<ChevronRightIcon
													aria-hidden
													className="size-3.5 shrink-0 text-fg4 group-data-selected/command-item:text-fg2"
												/>
											)}
										</CommandShortcut>
									</CommandItem>
								))}
							</CommandGroup>
						),
				)}
			</CommandList>
			<CommandFooter>
				<span className="flex items-center gap-1.5">
					<Kbd className={KEY_HINT}>↑</Kbd>
					<Kbd className={KEY_HINT}>↓</Kbd>
					to navigate
				</span>
				<span className="flex items-center gap-1.5">
					<Kbd className={KEY_HINT}>↵</Kbd>
					to open
				</span>
				<span className="flex items-center gap-1.5">
					<Kbd className={KEY_HINT}>esc</Kbd>
					{list === null ? "to close" : "to go back"}
				</span>
			</CommandFooter>
		</Command>
	);
}

const OpenPaletteContext = createContext<(() => void) | null>(null);
const PageItemsContext = createContext<((items: readonly PaletteItem[]) => void) | null>(null);

// Lists a page's own commands under This page while the page shows.
export function PageCommands({ items }: { items: readonly PageCommand[] }) {
	const setPageItems = use(PageItemsContext);
	if (setPageItems === null) throw new Error("PageCommands must be used inside CommandPaletteProvider");
	useEffect(() => {
		setPageItems(
			items.map((item) => ({
				...item,
				id: `this-page-${item.id}`,
				group: "This page",
			})),
		);
		return () => {
			setPageItems([]);
		};
	}, [setPageItems, items]);
	return null;
}

export function useOpenPalette(): () => void {
	const openPalette = use(OpenPaletteContext);
	if (openPalette === null) throw new Error("useOpenPalette must be used inside CommandPaletteProvider");
	return openPalette;
}

export function CommandPaletteProvider({ children }: { children: ReactNode }) {
	const system = useInSystem();
	const [open, setOpen] = useState(false);
	const [pageItems, setPageItems] = useState<readonly PaletteItem[]>([]);
	const items = usePaletteItems(open && !system, pageItems);
	const openPalette = useCallback(() => {
		setOpen(true);
	}, []);

	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			if (system) return;
			if (event.key.toLowerCase() !== SHORTCUT_KEY || !(event.metaKey || event.ctrlKey)) return;
			event.preventDefault();
			setOpen((was) => !was);
		};
		window.addEventListener("keydown", onKeyDown);
		return () => {
			window.removeEventListener("keydown", onKeyDown);
		};
	}, [system]);

	return (
		<OpenPaletteContext value={openPalette}>
			<PageItemsContext value={setPageItems}>{children}</PageItemsContext>
			<CommandDialog open={open && !system} onOpenChange={setOpen} title="Command palette" description={PLACEHOLDER}>
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

/** The command field in the top bar; an icon button below 760px. */
export function CommandMenu() {
	const openPalette = useOpenPalette();

	return (
		<button
			type="button"
			data-slot="command-button"
			aria-label="Search and run commands"
			aria-keyshortcuts="Meta+K Control+K"
			onClick={openPalette}
			className="-ml-0.5 flex size-7.5 shrink-0 items-center justify-center rounded-[7px] border border-line bg-panel text-[13px] text-fg3 transition-colors hover:border-line2 hover:text-fg2 min-[761px]:ml-0 min-[761px]:w-[360px] min-[761px]:shrink min-[761px]:justify-start min-[761px]:gap-2 min-[761px]:pr-1.5 min-[761px]:pl-2.5"
		>
			<SearchIcon aria-hidden className="size-3.5" />
			<span className="hidden min-w-0 flex-1 truncate text-left min-[761px]:block">Search or jump to…</span>
			<Kbd className="hidden bg-transparent text-[11px] text-fg3 min-[761px]:inline-flex">{SHORTCUT_LABEL}</Kbd>
		</button>
	);
}
