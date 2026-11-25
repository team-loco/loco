import { Code, ConnectError } from "@connectrpc/connect";
import { BadRequestSchema } from "@gen/google/rpc/error_details_pb";

import { AUDIT_EVENT_TYPES } from "@/pages/settings/auditEvents";
import { DEFAULT_RANGE, PAGE_SIZES, RANGES } from "./model";

export interface DroppedFilter {
	label: string;
	value: string;
	reason: string;
}

export function auditLink(
	search: string,
	workspaces: readonly string[] | undefined,
	resources: readonly string[] | undefined,
	environments: readonly string[] | undefined,
	selectedWorkspace = "",
	system = false,
) {
	const params = new URLSearchParams(search);
	const dropped: DroppedFilter[] = [];
	const drop = (key: string, label: string, reason: string) => {
		for (const value of params.getAll(key)) dropped.push({ label, value, reason });
		params.delete(key);
	};
	const range = params.get("range");
	if (range !== null && !RANGES.some((r) => r.value === range))
		drop("range", "Time range", `not a range; showing ${DEFAULT_RANGE.label}`);
	const size = params.get("size");
	if (size !== null && !PAGE_SIZES.some((s) => s.toString() === size))
		drop("size", "Rows per page", "use 50, 100 or 200; showing 50");
	const types = params.get("types");
	if (types !== null) {
		const values = types.split(",");
		const known = (v: string) => AUDIT_EVENT_TYPES.some((t) => t === v);
		for (const value of values.filter((v) => !known(v)))
			dropped.push({ label: "Event", value, reason: "no such event type" });
		const kept = values.filter(known);
		if (kept.length > 0) params.set("types", kept.join(","));
		else params.delete("types");
	}
	if (!system) {
		drop("org", "Organization", "only the System audit log filters by organization");
		drop("installation", "Installation only", "only the System audit log filters by installation");
	} else if (params.has("org") && params.has("installation")) {
		drop("installation", "Installation only", "an organization is selected; pick one or the other");
	}
	const workspace = params.get("ws");
	if (workspace !== null && workspaces !== undefined && !workspaces.includes(workspace))
		drop("ws", "Workspace", "no workspace with this name");
	for (const [key, label, noun, ids] of [
		["res", "Resource", "resource", resources],
		["env", "Environment", "environment", environments],
	] as const) {
		const value = params.get(key);
		if (value === null) continue;
		if (!params.has("ws") && selectedWorkspace === "") drop(key, label, `pick a workspace to filter by ${noun}`);
		else if (ids !== undefined && !ids.includes(value)) drop(key, label, `no ${noun} with this name`);
	}
	return { search: params.toString(), dropped };
}

export function auditFilterViolations(error: unknown) {
	if (!(error instanceof ConnectError) || error.code !== Code.InvalidArgument) return [];
	return error
		.findDetails(BadRequestSchema)
		.flatMap((d) => d.fieldViolations)
		.filter((v) => v.field !== "query");
}

export const AUDIT_FILTER_FIXES = new Map<
	string,
	{
		label: string;
		params: Partial<
			Record<"range" | "from" | "to" | "size" | "before" | "ws" | "res" | "env" | "org" | "installation", null>
		>;
	}
>([
	["start_time", { label: "Reset time range", params: { range: null, from: null, to: null } }],
	["end_time", { label: "Reset time range", params: { range: null, from: null, to: null } }],
	["page_size", { label: "Reset rows per page", params: { size: null } }],
	["page_token", { label: "Show newest events", params: { before: null } }],
	["org_id", { label: "Clear organization filter", params: { org: null } }],
	["installation_only", { label: "Clear installation filter", params: { installation: null } }],
	["workspace_id", { label: "Clear workspace filter", params: { ws: null, res: null, env: null } }],
	["resource_id", { label: "Clear resource filter", params: { res: null } }],
	["environment_id", { label: "Clear environment filter", params: { env: null } }],
]);
