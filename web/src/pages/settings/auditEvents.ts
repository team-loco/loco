import type { JsonObject } from "@bufbuild/protobuf";

export type AuditCategory = "access" | "organization" | "workspaces" | "deploys" | "tokens";

interface AuditType {
	label: string;
	category: AuditCategory;
}

const AUDIT_TYPES: Record<string, AuditType> = {
	"org.created": { label: "Created the organization", category: "organization" },
	"org.updated": { label: "Updated the organization", category: "organization" },
	"org.deleted": { label: "Deleted the organization", category: "organization" },
	"member.added": { label: "Added a member", category: "access" },
	"member.removed": { label: "Removed a member", category: "access" },
	"workspace.created": { label: "Created a workspace", category: "workspaces" },
	"workspace.updated": { label: "Updated a workspace", category: "workspaces" },
	"workspace.deleted": { label: "Deleted a workspace", category: "workspaces" },
	"environment.created": { label: "Created an environment", category: "workspaces" },
	"environment.updated": { label: "Updated an environment", category: "workspaces" },
	"environment.deleted": { label: "Deleted an environment", category: "workspaces" },
	"resource.created": { label: "Created a resource", category: "deploys" },
	"resource.updated": { label: "Updated a resource", category: "deploys" },
	"resource.deleted": { label: "Deleted a resource", category: "deploys" },
	"resource.scaled": { label: "Scaled a resource", category: "deploys" },
	"resource.env_updated": { label: "Changed environment variables", category: "deploys" },
	"deployment.created": { label: "Deployed", category: "deploys" },
	"deployment.deleted": { label: "Deleted a deployment", category: "deploys" },
	"build.created": { label: "Created a build", category: "deploys" },
	"build.started": { label: "Started a build", category: "deploys" },
	"build.canceled": { label: "Canceled a build", category: "deploys" },
	"domain.created": { label: "Added a custom domain", category: "deploys" },
	"domain.updated": { label: "Updated a custom domain", category: "deploys" },
	"domain.deleted": { label: "Removed a custom domain", category: "deploys" },
	"token.created": { label: "Created an API token", category: "tokens" },
	"token.revoked": { label: "Revoked an API token", category: "tokens" },
};

export const AUDIT_CATEGORIES: { value: AuditCategory | "all"; label: string }[] = [
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

export function auditLabel(type: string): string {
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
}

interface DetailFields {
	type: string;
	subjectId: string;
	data?: JsonObject | undefined;
}

function tokenActor(label: string, actorId: string, names: ReadonlyMap<string, string>): AuditActor {
	return { name: label, detail: names.get(actorId) ?? actorId };
}

export function auditActor(event: ActorFields, names: ReadonlyMap<string, string>): AuditActor {
	switch (event.actorType) {
		case "user": {
			if (event.actorName !== "") return { name: event.actorName, detail: event.actorEmail };
			if (event.actorEmail !== "") return { name: event.actorEmail, detail: "" };
			return { name: "Deleted user", detail: "" };
		}
		case "organization": {
			return tokenActor("Organization token", event.actorId, names);
		}
		case "workspace": {
			return tokenActor("Workspace token", event.actorId, names);
		}
		case "resource": {
			return tokenActor("Resource token", event.actorId, names);
		}
		case "system": {
			return { name: "Loco", detail: "" };
		}
		case "agent": {
			return { name: "Cluster agent", detail: "" };
		}
		case "anonymous": {
			return { name: "Anonymous", detail: "" };
		}
		default: {
			return { name: event.actorType, detail: event.actorId };
		}
	}
}

function text(data: JsonObject | undefined, key: string): string | undefined {
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
			return scopes.length === 0 ? member : `${member} · ${scopes.join(", ")}`;
		}
		case "member.removed": {
			return memberName(event.subjectId, users);
		}
		case "resource.env_updated": {
			return strings(data, "keys").join(", ");
		}
		default: {
			return text(data, "name") ?? text(data, "domain");
		}
	}
}
