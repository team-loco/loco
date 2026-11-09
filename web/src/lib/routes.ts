export function workspacePath(orgId: string, workspaceId: string, sub = ""): string {
	const base = `/org/${orgId}/wks/${workspaceId}`;
	return sub === "" ? base : `${base}/${sub}`;
}

export function resourcePath(orgId: string, workspaceId: string, resourceId: string): string {
	return workspacePath(orgId, workspaceId, `resource/${resourceId}`);
}

export const NEW_SERVICE_PARAM = "new";
export const NEW_SERVICE_VALUE = "1";

export function newServicePath(orgId: string, workspaceId: string, params: Record<string, string> = {}): string {
	const search = new URLSearchParams({ ...params, [NEW_SERVICE_PARAM]: NEW_SERVICE_VALUE });
	return `${workspacePath(orgId, workspaceId)}?${search.toString()}`;
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
