import { Scope } from "@gen/loco/token/v1/token_pb";
import {
	BoxesIcon,
	Building2Icon,
	ChartLineIcon,
	HistoryIcon,
	KeyRoundIcon,
	LayoutGridIcon,
	ServerIcon,
	ShieldCheckIcon,
	LockKeyholeIcon,
	ScrollTextIcon,
	Settings2Icon,
	UsersIcon,
	WaypointsIcon,
	WebhookIcon,
} from "lucide-react";
import type { ReactNode } from "react";
import { createLoader, parseAsString, useQueryStates } from "nuqs";
import { useLocation } from "react-router";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { useAuditAccess } from "@/hooks/useAuditAccess";
import { WINDOW_PARSERS } from "@/lib/obs-time";
import { useInSystem } from "@/hooks/useInSystem";
import {
	atPath,
	CLUSTERS_PATH,
	OPERATORS_PATH,
	ORGS_PATH,
	SYSTEM_AUDIT_PATH,
	SYSTEM_PATH,
	USERS_PATH,
} from "@/lib/system";
import { carriedQuery, QUERY_PARSERS, toQuery } from "@/pages/observability/query";
import {
	auditPath,
	secretsPath,
	webhooksPath,
	inResourcesSection,
	observabilityPath,
	observabilityViewParser,
	resourcesPath,
	SYSTEM_OBSERVABILITY_VIEWS,
	systemObservabilityPath,
	withEnv,
	workspacePath,
	type ObservabilityView,
} from "@/lib/routes";

export interface NavPage {
	name: string;
	icon: ReactNode;
	keywords: string;
	to: string | undefined;
	active: boolean;
}

export interface NavSection {
	label?: string;
	pages: NavPage[];
}

const OBSERVABILITY_PAGES: { view: ObservabilityView; name: string; icon: ReactNode }[] = [
	{ view: "logs", name: "Logs", icon: <ScrollTextIcon /> },
	{ view: "metrics", name: "Metrics", icon: <ChartLineIcon /> },
	{ view: "traces", name: "Traces", icon: <WaypointsIcon /> },
];

const NAV_PARAMS = { env: parseAsString, view: observabilityViewParser };
const loadCarried = createLoader({ ...WINDOW_PARSERS, ...QUERY_PARSERS });

function systemSections(pathname: string, search: string): NavSection[] {
	const current = SYSTEM_OBSERVABILITY_VIEWS.find((view) => atPath(pathname, `${SYSTEM_PATH}/${view}`));
	const { range, from, to, q, r } = loadCarried(search);
	const carriedTokens = toQuery({ q, r }).tokens;
	return [
		{
			pages: [
				{
					name: "Overview",
					icon: <LayoutGridIcon />,
					keywords: "system fleet health",
					to: SYSTEM_PATH,
					active: atPath(pathname, SYSTEM_PATH, true),
				},
			],
		},
		{
			label: "Fleet",
			pages: [
				{
					name: "Clusters",
					icon: <ServerIcon />,
					keywords: "system workers regions agents",
					to: CLUSTERS_PATH,
					active: atPath(pathname, CLUSTERS_PATH),
				},
			],
		},
		{
			label: "Tenants",
			pages: [
				{
					name: "Organizations",
					icon: <Building2Icon />,
					keywords: "system tenants customers limits suspend",
					to: ORGS_PATH,
					active: atPath(pathname, ORGS_PATH),
				},
			],
		},
		{
			label: "People",
			pages: [
				{
					name: "Users",
					icon: <UsersIcon />,
					keywords: "system accounts suspend sign-in",
					to: USERS_PATH,
					active: atPath(pathname, USERS_PATH),
				},
				{
					name: "Operators",
					icon: <ShieldCheckIcon />,
					keywords: "system roles admins readers grant revoke",
					to: OPERATORS_PATH,
					active: atPath(pathname, OPERATORS_PATH),
				},
			],
		},
		{
			label: "Activity",
			pages: [
				{
					name: "Audit log",
					icon: <HistoryIcon />,
					keywords: "system events activity history changes operators",
					to: SYSTEM_AUDIT_PATH,
					active: atPath(pathname, SYSTEM_AUDIT_PATH),
				},
			],
		},
		{
			label: "Observability",
			pages: OBSERVABILITY_PAGES.flatMap((page) =>
				page.view === "events"
					? []
					: [
							{
								name: page.name,
								icon: page.icon,
								keywords: "system observability telemetry",
								to: systemObservabilityPath(
									page.view,
									current !== undefined && current !== page.view
										? { range, from, to, q: carriedQuery(page.view, carriedTokens, true) }
										: {},
								),
								active: current === page.view,
							},
						],
			),
		},
	];
}

export function useNavSections(): NavSection[] {
	const { pathname, search } = useLocation();
	const [{ env, view }] = useQueryStates(NAV_PARAMS);
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();

	const envParams: Record<string, string> = env !== null ? { env } : {};
	const hasWs = activeOrgId !== null && activeWorkspaceId !== null;
	const wsBase = hasWs ? workspacePath(activeOrgId, activeWorkspaceId) : null;
	const onObs = wsBase !== null && pathname.startsWith(`${wsBase}/observability`);
	const { range, from, to, q, r } = loadCarried(search);
	const carried = { range, from, to };
	const carriedTokens = toQuery({ q, r }).tokens;
	const audit = useAuditAccess(activeOrgId);
	const scopes = useMyScopes();
	const inSystem = useInSystem();

	if (inSystem) return systemSections(pathname, search);

	return [
		{
			pages: [
				{
					name: "Dashboard",
					icon: <LayoutGridIcon />,
					keywords: "home overview",
					to: wsBase !== null ? withEnv(wsBase, env) : undefined,
					active: pathname === wsBase,
				},
				{
					name: "Resources",
					icon: <BoxesIcon />,
					keywords: "services inventory",
					to: hasWs ? withEnv(resourcesPath(activeOrgId, activeWorkspaceId), env) : undefined,
					active: hasWs && inResourcesSection(pathname, activeOrgId, activeWorkspaceId),
				},
			],
		},
		{
			label: "Observability",
			pages: OBSERVABILITY_PAGES.map((page) => ({
				name: page.name,
				icon: page.icon,
				keywords: "observability",
				to: hasWs
					? observabilityPath(
							activeOrgId,
							activeWorkspaceId,
							page.view,
							onObs && view !== page.view
								? { ...envParams, ...carried, q: carriedQuery(page.view, carriedTokens) }
								: envParams,
						)
					: undefined,
				active: onObs && view === page.view,
			})),
		},
		{
			label: "Workspace",
			pages: [
				...(hasWs && scopes.holds(activeOrgId, activeWorkspaceId, Scope.READ)
					? [
							{
								name: "Secrets",
								icon: <LockKeyholeIcon />,
								keywords: "environment credentials keys",
								to: withEnv(secretsPath(activeOrgId, activeWorkspaceId), env),
								active: pathname.endsWith("/secrets"),
							},
						]
					: []),
				...(hasWs && scopes.workspaceLevel(activeOrgId, activeWorkspaceId) >= scopeLevel(Scope.ADMIN)
					? [
							{
								name: "Webhooks",
								icon: <WebhookIcon />,
								keywords: "events subscriptions endpoints",
								to: webhooksPath(activeOrgId, activeWorkspaceId),
								active: pathname.endsWith("/webhooks"),
							},
						]
					: []),
				{
					name: "Settings",
					icon: <Settings2Icon />,
					keywords: "workspace environments",
					to: wsBase !== null ? withEnv(`${wsBase}/settings`, env) : undefined,
					active: pathname.endsWith("/settings"),
				},
			],
		},
		{
			label: "Organization",
			pages: [
				{
					name: "Team",
					icon: <UsersIcon />,
					keywords: "members people",
					to: activeOrgId !== null ? `/org/${activeOrgId}/team` : undefined,
					active: pathname.endsWith("/team"),
				},
				...(audit.visible && activeOrgId !== null
					? [
							{
								name: "Audit log",
								icon: <HistoryIcon />,
								keywords: "events activity history changes",
								to: auditPath(activeOrgId),
								active: pathname === auditPath(activeOrgId),
							},
						]
					: []),
				{
					name: "Tokens",
					icon: <KeyRoundIcon />,
					keywords: "api keys access",
					to: "/tokens",
					active: pathname === "/tokens",
				},
			],
		},
	];
}
