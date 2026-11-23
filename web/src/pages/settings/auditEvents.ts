import type { JsonObject, JsonValue } from "@bufbuild/protobuf";
import type { Event } from "@gen/loco/event/v1/event_pb";

export type AuditCategory = "access" | "organization" | "workspaces" | "deploys" | "tokens";

interface AuditType {
	label: string;
	category: AuditCategory;
}

const AUDIT_TYPES: Record<string, AuditType> = {
	"member.added": { label: "Added someone to a workspace", category: "access" },
	"member.removed": { label: "Removed someone from a workspace", category: "access" },
	"invitation.created": { label: "Invited someone", category: "access" },
	"invitation.resent": { label: "Resent an invitation", category: "access" },
	"invitation.claimed": { label: "Accepted an invitation", category: "access" },
	"invitation.revoked": { label: "Revoked an invitation", category: "access" },
	"webhook.test": { label: "Sent a test webhook", category: "workspaces" },
	"secret_key.rewrapped": {
		label: "Rewrapped an environment’s secret key",
		category: "deploys",
	},
	"org.created": {
		label: "Created the organization",
		category: "organization",
	},
	"org.updated": {
		label: "Updated the organization",
		category: "organization",
	},
	"org.deleted": {
		label: "Deleted the organization",
		category: "organization",
	},
	"org.suspended": {
		label: "Suspended the organization",
		category: "organization",
	},
	"org.resumed": {
		label: "Resumed the organization",
		category: "organization",
	},
	"org.limits_changed": {
		label: "Changed the organization's limits",
		category: "organization",
	},
	"org_member.added": {
		label: "Joined the organization",
		category: "access",
	},
	"org_member.role_changed": {
		label: "Changed an organization member’s role",
		category: "access",
	},
	"org_member.removed": {
		label: "Removed an organization member",
		category: "access",
	},
	"workspace.created": { label: "Created a workspace", category: "workspaces" },
	"workspace.updated": { label: "Updated a workspace", category: "workspaces" },
	"workspace.deleted": { label: "Deleted a workspace", category: "workspaces" },
	"environment.created": {
		label: "Created an environment",
		category: "workspaces",
	},
	"environment.updated": {
		label: "Updated an environment",
		category: "workspaces",
	},
	"environment.deleted": {
		label: "Deleted an environment",
		category: "workspaces",
	},
	"resource.created": { label: "Created a resource", category: "deploys" },
	"resource.updated": { label: "Updated a resource", category: "deploys" },
	"resource.deleted": { label: "Deleted a resource", category: "deploys" },
	"resource.scaled": { label: "Scaled a resource", category: "deploys" },
	"resource.restarted": { label: "Restarted a resource", category: "deploys" },
	"resource.partial_transferred": {
		label: "Moved a resource to another partial",
		category: "deploys",
	},
	"resource.degraded": { label: "Resource degraded", category: "deploys" },
	"resource.recovered": { label: "Resource recovered", category: "deploys" },
	"secret.set": { label: "Set secrets", category: "deploys" },
	"secret.deleted": { label: "Removed secrets", category: "deploys" },
	"deployment.created": { label: "Deployed", category: "deploys" },
	"deployment.canceled": { label: "Canceled a deployment", category: "deploys" },
	"deployment.deleted": { label: "Deleted a deployment", category: "deploys" },
	"build.created": { label: "Created a build", category: "deploys" },
	"build.started": { label: "Started a build", category: "deploys" },
	"build.succeeded": { label: "Build succeeded", category: "deploys" },
	"build.failed": { label: "Build failed", category: "deploys" },
	"build.canceled": { label: "Canceled a build", category: "deploys" },
	"domain.created": { label: "Added a domain", category: "deploys" },
	"domain.updated": { label: "Updated a domain", category: "deploys" },
	"domain.deleted": { label: "Removed a domain", category: "deploys" },
	"token.created": { label: "Created an API token", category: "tokens" },
	"token.revoked": { label: "Revoked an API token", category: "tokens" },
	"webhook.created": { label: "Added a webhook", category: "workspaces" },
	"webhook.updated": { label: "Updated a webhook", category: "workspaces" },
	"webhook.secret_rotated": {
		label: "Rotated a webhook secret",
		category: "workspaces",
	},
	"webhook.deleted": { label: "Removed a webhook", category: "workspaces" },
};

export const AUDIT_EVENT_TYPES: readonly string[] = Object.keys(AUDIT_TYPES);

export const AUDIT_CATEGORIES: {
	value: AuditCategory | "all";
	label: string;
}[] = [
	{ value: "all", label: "All" },
	{ value: "access", label: "Access" },
	{ value: "organization", label: "Organization" },
	{ value: "workspaces", label: "Workspaces" },
	{ value: "deploys", label: "Deploys" },
	{ value: "tokens", label: "Tokens" },
];

export function typesIn(category: AuditCategory | "all"): string[] {
	if (category === "all") return [];
	return Object.entries(AUDIT_TYPES)
		.filter(([, t]) => t.category === category)
		.map(([type]) => type);
}

// A platform domain is the resource's public address; the first one makes it public and removing it makes it internal.
const DOMAIN_LABELS: Record<string, { platform: [primary: string, other: string]; custom: string }> = {
	"domain.created": {
		platform: ["Made a resource public", "Added a platform domain"],
		custom: "Added a custom domain",
	},
	"domain.updated": {
		platform: ["Updated a platform domain", "Updated a platform domain"],
		custom: "Updated a custom domain",
	},
	"domain.deleted": {
		platform: ["Made a resource internal", "Removed a platform domain"],
		custom: "Removed a custom domain",
	},
};

const RETENTION_FIELDS: [field: string, name: string][] = [
	["log_retention_days", "log retention"],
	["trace_retention_days", "trace retention"],
	["metric_retention_days", "metric retention"],
];

interface WorkspaceChange {
	label: string;
	field: string;
	values: string;
}

function days(value: JsonValue | undefined): string {
	return typeof value === "number" ? `${value.toString()} days` : "org limit";
}

function retentionValues(before: JsonValue | undefined, after: JsonValue | undefined): string {
	if (typeof before === "number" && typeof after === "number") return `${before.toString()} → ${days(after)}`;
	return `${days(before)} → ${days(after)}`;
}

function object(data: JsonObject | undefined, key: string): JsonObject {
	const value = data?.[key];
	return typeof value === "object" && value !== null && !Array.isArray(value) ? value : {};
}

/** The fields a workspace.updated event's old and new values show changing. */
function workspaceChanges(data: JsonObject | undefined): WorkspaceChange[] {
	const before = object(data, "old");
	const after = object(data, "new");
	const changed = (key: string) => key in after && before[key] !== after[key];
	const changes: WorkspaceChange[] = [];
	if (changed("name")) {
		changes.push({
			label: "Renamed workspace",
			field: "name",
			values: `${text(before, "name") ?? ""} → ${text(after, "name") ?? ""}`,
		});
	}
	if (changed("description")) {
		changes.push({ label: "Changed the workspace description", field: "description", values: "" });
	}
	for (const [key, name] of RETENTION_FIELDS) {
		if (changed(key)) {
			changes.push({ label: `Changed ${name}`, field: name, values: retentionValues(before[key], after[key]) });
		}
	}
	return changes;
}

export function auditLabel(type: string, data?: JsonObject): string {
	if (type === "workspace.updated") {
		const changes = workspaceChanges(data);
		if (changes.length === 1 && changes[0] !== undefined) return changes[0].label;
	}
	const domain = DOMAIN_LABELS[type];
	const source = text(data, "domainSource");
	if (domain !== undefined && source !== undefined) {
		if (source === "user_provided") return domain.custom;
		return domain.platform[data?.["primary"] === true ? 0 : 1];
	}
	return AUDIT_TYPES[type]?.label ?? type;
}

export interface AuditUser {
	name: string;
	email: string;
}

export interface AuditActor {
	name: string;
	detail: string;
}

interface ActorFields {
	actorType: string;
	actorId: string;
	actorName: string;
	actorEmail: string;
	actorTokenType: string;
}

interface DetailFields {
	type: string;
	subjectId: string;
	data?: JsonObject | undefined;
}

const TOKEN_KINDS: Record<string, string> = {
	user: "Personal token",
	organization: "Organization token",
	workspace: "Workspace token",
	resource: "Resource token",
};

export function tokenKind(event: { actorTokenType: string }): string {
	return TOKEN_KINDS[event.actorTokenType] ?? "API token";
}

export function auditActor(event: ActorFields): AuditActor {
	switch (event.actorType) {
		case "user": {
			if (event.actorName !== "") return { name: event.actorName, detail: event.actorEmail };
			if (event.actorEmail !== "") return { name: event.actorEmail, detail: "" };
			return { name: "Deleted user", detail: "" };
		}
		case "api_token": {
			return { name: event.actorName === "" ? "Deleted token" : event.actorName, detail: tokenKind(event) };
		}
		case "system": {
			return { name: "Loco", detail: "" };
		}
		case "agent": {
			return { name: "Cluster agent", detail: "" };
		}
		default: {
			return { name: event.actorType, detail: event.actorId };
		}
	}
}

export function text(data: JsonObject | undefined, key: string): string | undefined {
	const value = data?.[key];
	if (typeof value === "string" && value !== "") return value;
	if (typeof value === "number" || typeof value === "boolean") return String(value);
	return undefined;
}

function strings(data: JsonObject | undefined, key: string): string[] {
	const value = data?.[key];
	return Array.isArray(value) ? value.filter((v) => typeof v === "string") : [];
}

function memberName(subjectId: string, users: ReadonlyMap<string, AuditUser>): string {
	const user = users.get(subjectId);
	if (user === undefined) return subjectId;
	if (user.name !== "") return user.name;
	if (user.email !== "") return user.email;
	return subjectId;
}

export function auditDetail(event: DetailFields, users: ReadonlyMap<string, AuditUser>): string | undefined {
	const { data } = event;
	switch (event.type) {
		case "member.added": {
			const scopes = strings(data, "scopes");
			const member = memberName(event.subjectId, users);
			return scopes.length === 0 ? member : `${member} as ${scopes.join(", ")}`;
		}
		case "member.removed": {
			return memberName(event.subjectId, users);
		}
		case "org_member.added": {
			return memberName(event.subjectId, users);
		}
		case "org_member.removed": {
			return memberName(event.subjectId, users);
		}
		case "secret.set": {
			return strings(data, "names").join(", ");
		}
		case "secret.deleted": {
			return strings(data, "names").join(", ");
		}
		case "workspace.updated": {
			const changes = workspaceChanges(data);
			if (changes.length === 1) return changes[0]?.values;
			return changes.map((c) => `${c.field} ${c.values}`.trim()).join(", ");
		}
		default: {
			return text(data, "name") ?? text(data, "domain") ?? text(data, "email") ?? text(data, "url");
		}
	}
}

export function distinctAuditEvents(events: readonly Event[]): Event[] {
	const seen = new Set<string>();
	return events.filter((event) => {
		if (seen.has(event.id)) return false;
		seen.add(event.id);
		return true;
	});
}
