import { matchPath } from "react-router";

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

export type SettingsScope = "org" | "workspace" | "account";

export function settingsPath(orgId: string, workspaceId: string, scope: SettingsScope): string {
	return `${workspacePath(orgId, workspaceId, "settings")}?scope=${scope}`;
}

export const PROFILE_PATH = "/profile";

export function accountSettingsPath(orgId: string | null, workspaceId: string | null): string {
	return orgId !== null && workspaceId !== null ? settingsPath(orgId, workspaceId, "account") : PROFILE_PATH;
}

export function resourcePath(orgId: string, workspaceId: string, resourceId: string): string {
	return workspacePath(orgId, workspaceId, `resource/${resourceId}`);
}

export function newResourcePath(orgId: string, workspaceId: string, params: Record<string, string> = {}): string {
	const search = new URLSearchParams(params).toString();
	return `${workspacePath(orgId, workspaceId, "new")}${search === "" ? "" : `?${search}`}`;
}

export type ObservabilityView = "logs" | "metrics" | "traces" | "events";

export function observabilityPath(
	orgId: string,
	workspaceId: string,
	view: ObservabilityView,
	params: Record<string, string> = {},
): string {
	const search = new URLSearchParams({ view, ...params });
	return `${workspacePath(orgId, workspaceId, "observability")}?${search.toString()}`;
}
