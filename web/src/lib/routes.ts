import { createSerializer, parseAsString, parseAsStringLiteral } from "nuqs";
import { matchPath } from "react-router";

import { formatSpan, QUERY_MAX_MS } from "./obs-time";

export function workspacePath(orgId: string, workspaceId: string, sub = ""): string {
	const base = `/org/${orgId}/wks/${workspaceId}`;
	return sub === "" ? base : `${base}/${sub}`;
}

export const WORKSPACE_ROUTE = "/org/:orgId/wks/:workspaceId/*";

export function switchWorkspacePath(pathname: string, search: string, orgId: string, workspaceId: string): string {
	const rest = matchPath(WORKSPACE_ROUTE, pathname)?.params["*"] ?? "";
	if (rest === "" || rest.startsWith("resource/")) return workspacePath(orgId, workspaceId);
	const params = new URLSearchParams(search);
	params.delete("env");
	const query = params.toString();
	return `${workspacePath(orgId, workspaceId, rest)}${query === "" ? "" : `?${query}`}`;
}

export const SETTINGS_SCOPES = ["org", "workspace", "account"] as const;
export type SettingsScope = (typeof SETTINGS_SCOPES)[number];

export function settingsPath(orgId: string, workspaceId: string, scope: SettingsScope): string {
	return `${workspacePath(orgId, workspaceId, "settings")}?scope=${scope}`;
}

export function orgSettingsPath(orgId: string, workspaceId: string | null): string {
	return workspaceId !== null ? settingsPath(orgId, workspaceId, "org") : `/org/${orgId}/settings`;
}

export function auditPath(orgId: string): string {
	return `/org/${orgId}/audit`;
}

export const PROFILE_PATH = "/profile";

export function accountSettingsPath(orgId: string | null, workspaceId: string | null): string {
	return orgId !== null && workspaceId !== null ? settingsPath(orgId, workspaceId, "account") : PROFILE_PATH;
}

export function resourcePath(orgId: string, workspaceId: string, resourceId: string): string {
	return workspacePath(orgId, workspaceId, `resource/${resourceId}`);
}

export function resourcesPath(orgId: string, workspaceId: string): string {
	return workspacePath(orgId, workspaceId, "resources");
}

export function inResourcesSection(pathname: string, orgId: string, workspaceId: string): boolean {
	const base = workspacePath(orgId, workspaceId);
	return pathname === resourcesPath(orgId, workspaceId) || pathname.startsWith(`${base}/resource/`);
}

export const NEW_RESOURCE_PARAM = "new";
const NEW_RESOURCE_FLAG = "1";

export function newResourcePath(orgId: string, workspaceId: string, params: Record<string, string> = {}): string {
	const search = new URLSearchParams(params);
	search.set(NEW_RESOURCE_PARAM, NEW_RESOURCE_FLAG);
	return `${resourcesPath(orgId, workspaceId)}?${search.toString()}`;
}

export const DRAFT_PARAM = "draft";

const draftSerializer = createSerializer({ [NEW_RESOURCE_PARAM]: parseAsString, [DRAFT_PARAM]: parseAsString });

export function draftSearch(search: string | URLSearchParams, draftId: string): string {
	return draftSerializer(search, { [NEW_RESOURCE_PARAM]: null, [DRAFT_PARAM]: draftId });
}

export const RESOURCE_VIEWS = ["table", "map"] as const;
export type ResourceView = (typeof RESOURCE_VIEWS)[number];
export const resourceViewParser = parseAsStringLiteral(RESOURCE_VIEWS).withDefault("table");

export const OBSERVABILITY_VIEWS = ["logs", "metrics", "traces", "events"] as const;
export type ObservabilityView = (typeof OBSERVABILITY_VIEWS)[number];
export const observabilityViewParser = parseAsStringLiteral(OBSERVABILITY_VIEWS).withDefault("logs");

export function observabilityPath(
	orgId: string,
	workspaceId: string,
	view: ObservabilityView,
	params: Record<string, string> = {},
): string {
	const search = new URLSearchParams({ view, ...params });
	return `${workspacePath(orgId, workspaceId, "observability")}?${search.toString()}`;
}

export function buildLogsPath(orgId: string, workspaceId: string, buildId: string): string {
	return observabilityPath(orgId, workspaceId, "logs", { q: `build:${buildId}`, range: formatSpan(QUERY_MAX_MS) });
}
