import {
	BellIcon,
	BoxesIcon,
	ChartLineIcon,
	KeyRoundIcon,
	LayoutGridIcon,
	ScrollTextIcon,
	Settings2Icon,
	UsersIcon,
	WaypointsIcon,
} from "lucide-react";
import type { ReactNode } from "react";
import { useLocation, useSearchParams } from "react-router";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { pageImporters } from "@/lib/lazy-pages";
import {
	inResourcesSection,
	observabilityPath,
	resourcesPath,
	workspacePath,
	type ObservabilityView,
} from "@/lib/routes";

export interface NavPage {
	name: string;
	icon: ReactNode;
	keywords: string;
	to: string | undefined;
	active: boolean;
	preload: () => Promise<unknown>;
}

export interface NavSection {
	label?: string;
	pages: NavPage[];
}

const OBSERVABILITY_PAGES: { view: ObservabilityView; name: string; icon: ReactNode }[] = [
	{ view: "logs", name: "Logs", icon: <ScrollTextIcon /> },
	{ view: "metrics", name: "Metrics", icon: <ChartLineIcon /> },
	{ view: "traces", name: "Traces", icon: <WaypointsIcon /> },
	{ view: "events", name: "Events", icon: <BellIcon /> },
];

export function useNavSections(): NavSection[] {
	const { pathname } = useLocation();
	const [params] = useSearchParams();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();

	const env = params.get("env");
	const envParams: Record<string, string> = env !== null ? { env } : {};
	const withEnv = (path: string) => (env !== null ? `${path}?env=${encodeURIComponent(env)}` : path);
	const view = params.get("view") ?? "logs";
	const hasWs = activeOrgId !== null && activeWorkspaceId !== null;
	const wsBase = hasWs ? workspacePath(activeOrgId, activeWorkspaceId) : null;
	const onObs = wsBase !== null && pathname.startsWith(`${wsBase}/observability`);

	return [
		{
			pages: [
				{
					name: "Dashboard",
					icon: <LayoutGridIcon />,
					keywords: "home overview",
					to: wsBase !== null ? withEnv(wsBase) : undefined,
					active: pathname === wsBase,
					preload: pageImporters.Dashboard,
				},
				{
					name: "Resources",
					icon: <BoxesIcon />,
					keywords: "services inventory",
					to: hasWs ? withEnv(resourcesPath(activeOrgId, activeWorkspaceId)) : undefined,
					active: hasWs && inResourcesSection(pathname, activeOrgId, activeWorkspaceId),
					preload: pageImporters.Resources,
				},
			],
		},
		{
			label: "Observability",
			pages: OBSERVABILITY_PAGES.map((page) => ({
				name: page.name,
				icon: page.icon,
				keywords: "observability",
				to: hasWs ? observabilityPath(activeOrgId, activeWorkspaceId, page.view, envParams) : undefined,
				active: onObs && view === page.view,
				preload: pageImporters.Observability,
			})),
		},
		{
			label: "Manage",
			pages: [
				{
					name: "Tokens",
					icon: <KeyRoundIcon />,
					keywords: "api keys access",
					to: "/tokens",
					active: pathname === "/tokens",
					preload: pageImporters.Tokens,
				},
				{
					name: "Team",
					icon: <UsersIcon />,
					keywords: "members people",
					to: activeOrgId !== null ? `/org/${activeOrgId}/team` : undefined,
					active: pathname.endsWith("/team"),
					preload: pageImporters.Team,
				},
				{
					name: "Settings",
					icon: <Settings2Icon />,
					keywords: "workspace environments",
					to: wsBase !== null ? withEnv(`${wsBase}/settings`) : undefined,
					active: pathname.endsWith("/settings"),
					preload: pageImporters.Settings,
				},
			],
		},
	];
}
