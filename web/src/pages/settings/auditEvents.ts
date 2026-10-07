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
	"org_domain.added": { label: "Added a domain", category: "access" },
	"org_domain.verified": { label: "Verified a domain", category: "access" },
	"org_domain.auto_join_changed": { label: "Changed domain auto-join", category: "access" },
	"org_domain.removed": { label: "Removed a domain", category: "access" },
	"org_sso.configured": { label: "Connected SSO", category: "access" },
	"org_sso.require_changed": { label: "Changed the SSO requirement", category: "access" },
	"org_sso.removed": { label: "Removed SSO", category: "access" },
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

function text(data: JsonObject | undefined, key: string): string | undefined {
	const value = data?.[key];
	if (typeof value === "string" && value !== "") return value;
	if (typeof value === "number" || typeof value === "boolean") return String(value);
	return undefined;
}

export function auditDetail(type: string, data: JsonObject | undefined): string | undefined {
	switch (type) {
		case "org_domain.auto_join_changed": {
			const domain = text(data, "domain") ?? "";
			const scope = text(data, "scope");
			return scope === undefined ? `${domain} · off` : `${domain} · ${scope}`;
		}
		case "org_sso.configured": {
			const domains = data?.domains;
			return Array.isArray(domains) ? domains.filter((d) => typeof d === "string").join(", ") : undefined;
		}
		case "org_sso.require_changed": {
			return data?.requireSso === true ? "Required" : "Optional";
		}
		case "member.added": {
			return text(data, "scope");
		}
		case "resource.env_updated": {
			const keys = data?.keys;
			return Array.isArray(keys) ? keys.filter((k) => typeof k === "string").join(", ") : undefined;
		}
		default: {
			return text(data, "name") ?? text(data, "domain");
		}
	}
}
