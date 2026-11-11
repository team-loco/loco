import { ResourceStatus, type RegionInfo } from "@gen/loco/resource/v1/resource_pb";

import type { NewResourceForm } from "@/lib/new-resource";
import { statusRank } from "@/lib/resource-status";

export type DraftEdits = Partial<Omit<NewResourceForm, "image" | "name">>;

export interface Draft {
	id: string;
	image: string;
	name: string;
	createdMs: number;
	edits: DraftEdits;
}

const KEY_PREFIX = "loco:drafts:v1";

export const DRAFT_NOTE = "Not deployed yet";

export const DRAFT_RANK = (statusRank(ResourceStatus.DEPLOYING) + statusRank(ResourceStatus.UNSPECIFIED)) / 2;

export function draftsKey(orgId: string, workspaceId: string, environmentId: string): string {
	return `${KEY_PREFIX}:${orgId}:${workspaceId}:${environmentId}`;
}

function isDraft(value: unknown): value is Draft {
	if (typeof value !== "object" || value === null) return false;
	const d = value as Record<string, unknown>;
	return (
		typeof d["id"] === "string" &&
		typeof d["image"] === "string" &&
		typeof d["name"] === "string" &&
		typeof d["createdMs"] === "number" &&
		typeof d["edits"] === "object" &&
		d["edits"] !== null
	);
}

export function parseDrafts(value: unknown): Draft[] {
	return Array.isArray(value) ? value.filter(isDraft) : [];
}

export function addDraft(drafts: readonly Draft[], draft: Draft): Draft[] {
	return [...drafts, draft];
}

export function editDraft(drafts: readonly Draft[], id: string, edits: DraftEdits): Draft[] {
	const stored =
		edits.vars === undefined
			? edits
			: {
					...edits,
					vars: edits.vars.map((row) => (row.secret ? { ...row, value: "" } : row)),
				};
	return drafts.map((d) => (d.id === id ? { ...d, edits: { ...d.edits, ...stored } } : d));
}

export function removeDraft(drafts: readonly Draft[], id: string): Draft[] {
	return drafts.filter((d) => d.id !== id);
}

export function draftRegions(draft: Draft, regions: readonly RegionInfo[]): string[] {
	return draft.edits.regions ?? regions.filter((r) => r.isDefault).map((r) => r.region);
}

export function draftIsPublic(draft: Draft): boolean {
	return draft.edits.isPublic ?? true;
}

export interface ServiceSizing {
	cpu: string;
	memory: string;
	minReplicas: number;
	cpuTarget: number;
}

export function draftSizing(draft: Draft, defaults: ServiceSizing) {
	const replicas = draft.edits.replicas ?? defaults.minReplicas;
	return {
		cpu: draft.edits.cpu ?? defaults.cpu,
		memory: draft.edits.memory ?? defaults.memory,
		replicas,
		maxReplicas: draft.edits.maxReplicas ?? replicas,
		cpuTarget: draft.edits.cpuTarget ?? defaults.cpuTarget,
	};
}
