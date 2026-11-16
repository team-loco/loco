import { Code, ConnectError } from "@connectrpc/connect";
import { BadRequestSchema } from "@gen/google/rpc/error_details_pb";
import { createParser } from "nuqs";

import { AUDIT_EVENT_TYPES } from "@/pages/settings/auditEvents";
import { PAGE_SIZES, RANGES } from "./model";

export const sequenceParser = createParser<bigint>({
	parse: (value) =>
		/^\d+$/.test(value) && BigInt(value) > 0n && BigInt(value) <= 9223372036854775807n ? BigInt(value) : null,
	serialize: (value) => value.toString(),
});

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
) {
	const params = new URLSearchParams(search);
	const dropped: DroppedFilter[] = [];
	const drop = (key: string, label: string, reason: string) => {
		for (const value of params.getAll(key)) dropped.push({ label, value, reason });
		params.delete(key);
	};
	const range = params.get("range");
	if (range !== null && !RANGES.some((r) => r.value === range)) drop("range", "Time range", "Unknown time range");
	const size = params.get("size");
	if (size !== null && !PAGE_SIZES.some((s) => s.toString() === size))
		drop("size", "Rows per page", `Use ${PAGE_SIZES.join(", ")}`);
	for (const [key, label, valid, reason] of [
		["types", "Event", (v: string) => AUDIT_EVENT_TYPES.some((t) => t === v), "Unknown event type"],
		["before", "Page", (v: string) => sequenceParser.parse(v) !== null, "Invalid page position"],
	] as const) {
		const raw = params.get(key);
		if (raw === null) continue;
		const values = raw.split(",");
		const kept = values.filter(valid);
		for (const value of values.filter((v) => !valid(v))) dropped.push({ label, value, reason });
		if (kept.length > 0) params.set(key, kept.join(","));
		else params.delete(key);
	}
	const workspace = params.get("ws");
	if (workspace !== null && workspaces !== undefined && !workspaces.includes(workspace))
		drop("ws", "Workspace", "Workspace not found");
	for (const [key, label, ids] of [
		["res", "Resource", resources],
		["env", "Environment", environments],
	] as const) {
		const value = params.get(key);
		if (value === null) continue;
		if (!params.has("ws") && selectedWorkspace === "") drop(key, label, "Choose a workspace first");
		else if (ids !== undefined && !ids.includes(value)) drop(key, label, `${label} not found in this workspace`);
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
	{ label: string; params: Partial<Record<"range" | "size" | "before" | "ws" | "res" | "env", null>> }
>([
	["start_time", { label: "Reset time range", params: { range: null } }],
	["end_time", { label: "Reset time range", params: { range: null } }],
	["page_size", { label: "Reset rows per page", params: { size: null } }],
	["before_seq", { label: "Show newest events", params: { before: null } }],
	["workspace_id", { label: "Clear workspace filter", params: { ws: null, res: null, env: null } }],
	["resource_id", { label: "Clear resource filter", params: { res: null } }],
	["environment_id", { label: "Clear environment filter", params: { env: null } }],
]);
