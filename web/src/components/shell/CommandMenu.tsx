import { Scope } from "@gen/loco/token/v1/token_pb";
import { useQuery } from "@tanstack/react-query";
import {
	Building2Icon,
	CornerDownLeftIcon,
	LayersIcon,
	PackageIcon,
	PlusIcon,
	SearchIcon,
	UserIcon,
	UserPlusIcon,
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
import { ModeIcon } from "@/components/design/ThemePreview";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import { scorePaletteItem } from "@/lib/command-palette";
import { accountSettingsPath, newResourcePath, resourcePath, switchWorkspacePath } from "@/lib/routes";
import { modes, nextMode } from "@/lib/theme";
import { useTheme } from "@/lib/use-theme";

import { useNavSections } from "./nav";

const SHORTCUT_KEY = "k";
const IS_MAC = /Mac|iPhone|iPad/.test(navigator.userAgent);
export const SHORTCUT_LABEL = IS_MAC ? "⌘K" : "Ctrl K";
const PLACEHOLDER = "Search resources, pages and actions";
const PALETTE_GROUPS = ["Actions", "Resources", "Pages", "Workspaces"] as const;
const BROWSE_LIMIT = 8;

interface PaletteItem {
	id: string;
	group: (typeof PALETTE_GROUPS)[number];
	label: string;
	keywords?: string;
	meta?: string | undefined;
	icon: ReactNode;
	run: () => void;
}

function usePaletteItems(open: boolean): PaletteItem[] {
	const navigate = useNavigate();
	const { pathname, search } = useLocation();
	const [params] = useSearchParams();
	const { mode, toggle } = useTheme();
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
				icon: <PackageIcon />,
				run: go(resourcePath(orgId, workspaceId, r.id)),
			});
		}
	}

	items.push({
		id: "action-theme",
		group: "Actions",
		label: `Switch to ${modes.find((m) => m.id === nextMode(mode))?.name.toLowerCase() ?? ""} mode`,
		keywords: "theme appearance toggle",
		icon: <ModeIcon mode={nextMode(mode)} />,
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
	if (activeOrgId !== null && scopes.holds(activeOrgId, "", Scope.ADMIN)) {
		items.push({
			id: "action-invite",
			group: "Actions",
			label: "Invite member",
			keywords: "team people email add",
			icon: <UserPlusIcon />,
			run: go(`/org/${activeOrgId}/team?invite=1`),
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

	return items;
}

function PaletteBody({ items, onClose }: { items: readonly PaletteItem[]; onClose: () => void }) {
	const [query, setQuery] = useState("");
	const browsing = query.trim() === "";
	const groups = PALETTE_GROUPS.filter((group) => !browsing || group !== "Workspaces").map((group) => {
		const inGroup = items.filter((item) => item.group === group);
		return { group, items: browsing ? inGroup.slice(0, BROWSE_LIMIT) : inGroup };
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
