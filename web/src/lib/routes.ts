export function workspacePath(orgId: string, workspaceId: string, sub = ""): string {
	const base = `/org/${orgId}/wks/${workspaceId}`;
	return sub === "" ? base : `${base}/${sub}`;
}

export function resourcePath(orgId: string, workspaceId: string, resourceId: string): string {
	return workspacePath(orgId, workspaceId, `resource/${resourceId}`);
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
