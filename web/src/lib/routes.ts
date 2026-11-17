import { createSerializer, type inferParserType, parseAsArrayOf, parseAsString, parseAsStringLiteral } from "nuqs";
import { matchPath } from "react-router";

import { msParser, WINDOW_PARSERS } from "./obs-time";
import { DAY_MS } from "./time";

export function workspacePath(orgId: string, workspaceId: string, sub = ""): string {
	const base = `/org/${orgId}/wks/${workspaceId}`;
	return sub === "" ? base : `${base}/${sub}`;
}

export const WORKSPACE_ROUTE = "/org/:orgId/wks/:workspaceId/*";

const serializeEnv = createSerializer({ env: parseAsString });

export function switchWorkspacePath(pathname: string, search: string, orgId: string, workspaceId: string): string {
	const rest = matchPath(WORKSPACE_ROUTE, pathname)?.params["*"] ?? "";
	if (rest === "" || rest.startsWith("resource/")) return workspacePath(orgId, workspaceId);
	return `${workspacePath(orgId, workspaceId, rest)}${serializeEnv(search, { env: null })}`;
}

export const SETTINGS_SCOPES = ["org", "workspace", "account"] as const;
export type SettingsScope = (typeof SETTINGS_SCOPES)[number];

const serializeScope = createSerializer({ scope: parseAsStringLiteral(SETTINGS_SCOPES) });

export function settingsPath(orgId: string, workspaceId: string, scope: SettingsScope): string {
	return `${workspacePath(orgId, workspaceId, "settings")}${serializeScope({ scope })}`;
}

export function orgSettingsPath(orgId: string, workspaceId: string | null): string {
	return workspaceId !== null ? settingsPath(orgId, workspaceId, "org") : `/org/${orgId}/settings`;
}

export function auditPath(orgId: string): string {
	return `/org/${orgId}/audit`;
}

export function webhooksPath(orgId: string, workspaceId: string, types?: string): string {
	const path = workspacePath(orgId, workspaceId, "webhooks");
	return types === undefined ? path : `${path}?${new URLSearchParams({ new: "1", types })}`;
}

export const PROFILE_PATH = "/profile";

export function accountSettingsPath(orgId: string | null, workspaceId: string | null): string {
	return orgId !== null && workspaceId !== null ? settingsPath(orgId, workspaceId, "account") : PROFILE_PATH;
}

const serializeResourceLink = createSerializer({ tab: parseAsString, env: parseAsString });

export function resourcePath(
	orgId: string,
	workspaceId: string,
	resourceId: string,
	link: { tab?: string; env?: string | null } = {},
): string {
	return serializeResourceLink(workspacePath(orgId, workspaceId, `resource/${resourceId}`), link);
}

export function withEnv(path: string, env: string | null): string {
	return serializeEnv(path, { env });
}

export function resourcesPath(orgId: string, workspaceId: string): string {
	return workspacePath(orgId, workspaceId, "resources");
}

export function inResourcesSection(pathname: string, orgId: string, workspaceId: string): boolean {
	const base = workspacePath(orgId, workspaceId);
	return pathname === resourcesPath(orgId, workspaceId) || pathname.startsWith(`${base}/resource/`);
}

export const NEW_RESOURCE_PARAM = "new";
export const DRAFT_PARAM = "draft";

const serializeNewResource = createSerializer({ [NEW_RESOURCE_PARAM]: parseAsString, [DRAFT_PARAM]: parseAsString });

export function newResourcePath(
	orgId: string,
	workspaceId: string,
	params: string | Record<string, string> = {},
): string {
	return `${resourcesPath(orgId, workspaceId)}${serializeNewResource(new URLSearchParams(params), { [NEW_RESOURCE_PARAM]: "1" })}`;
}

export function draftSearch(search: string | URLSearchParams, draftId: string): string {
	return serializeNewResource(search, { [NEW_RESOURCE_PARAM]: null, [DRAFT_PARAM]: draftId });
}

export const RESOURCE_VIEWS = ["table", "map"] as const;
export type ResourceView = (typeof RESOURCE_VIEWS)[number];
export const resourceViewParser = parseAsStringLiteral(RESOURCE_VIEWS).withDefault("table");

export const OBSERVABILITY_VIEWS = ["logs", "metrics", "traces", "events"] as const;
export type ObservabilityView = (typeof OBSERVABILITY_VIEWS)[number];
export const observabilityViewParser = parseAsStringLiteral(OBSERVABILITY_VIEWS).withDefault("logs");

const OBSERVABILITY_LINK = {
	env: parseAsString,
	...WINDOW_PARSERS,
	q: parseAsString,
	r: parseAsArrayOf(parseAsString),
	at: msParser,
	trace: parseAsString,
	span: parseAsString,
};

const serializeObservability = createSerializer({
	view: parseAsStringLiteral(OBSERVABILITY_VIEWS),
	...OBSERVABILITY_LINK,
});

export type ObservabilityLink = Partial<inferParserType<typeof OBSERVABILITY_LINK>>;

export function observabilityPath(
	orgId: string,
	workspaceId: string,
	view: ObservabilityView,
	link: ObservabilityLink = {},
): string {
	return `${workspacePath(orgId, workspaceId, "observability")}${serializeObservability({ view, ...link })}`;
}

export function buildLogsPath(orgId: string, workspaceId: string, buildId: string): string {
	return observabilityPath(orgId, workspaceId, "logs", { q: `build:${buildId}`, range: DAY_MS });
}
