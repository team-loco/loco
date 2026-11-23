import type { Secret } from "@gen/loco/secret/v1/secret_pb";

import { dotEnvNotes, parseDotEnv } from "@/lib/dotenv";
import { referenceOf } from "@/pages/resource/variables";

export const SECRET_NAME = /^[A-Z_][A-Z0-9_]*$/;
const USER_ENTITY = "user";
const SHORT_ID = 8;

function list(names: string[]): string {
	return new Intl.ListFormat("en", { style: "long", type: "conjunction" }).format(names);
}

export function secretNameProblem(name: string, taken: boolean, env: string): string {
	if (name === "") return "";
	if (!SECRET_NAME.test(name)) return "Use capital letters, digits and underscores, not starting with a digit.";
	if (taken) return `${name} already exists in ${env}.`;
	return "";
}

/** The note under the value of a secret being created or updated. */
export function secretHint(name: string, users: string[], updating: boolean): string {
	if (!updating)
		return `Refer to it from a resource’s variables as ${referenceOf("secrets", name === "" ? "NAME" : name)}.`;
	if (users.length === 0) return "No resource refers to it yet.";
	const one = users.length === 1;
	return `${list(users)} ${one ? "gets" : "get"} the new value on ${one ? "its" : "their"} next deploy or restart.`;
}

export function deleteBody(name: string): string {
	return `No resource refers to ${name}. This can’t be undone.`;
}

/** Why a secret cannot be deleted while resources read it. */
export function deleteBlocked(users: string[]): string {
	return `Read by ${list(users)}. Remove the reference and redeploy first.`;
}

export interface ImportPreview {
	values: Record<string, string>;
	invalid: string[];
	summary: string;
	notes: string[];
}

export function importPreview(text: string, existing: ReadonlySet<string>): ImportPreview {
	const parsed = parseDotEnv(text);
	const pairs = parsed.pairs;
	const invalid = pairs.filter(([name]) => !SECRET_NAME.test(name)).map(([name]) => name);
	const values = Object.fromEntries(pairs);
	const replacing = Object.keys(values).filter((name) => existing.has(name)).length;
	const count = Object.keys(values).length;
	const summary =
		invalid.length > 0
			? `Not valid secret names: ${invalid.join(", ")}`
			: count === 0
				? "Nothing found yet"
				: `${count.toString()} found${replacing > 0 ? `, ${replacing.toString()} replace existing` : ""}`;
	return { values, invalid, summary, notes: dotEnvNotes(parsed, existing) };
}

export function setterLabel(secret: Secret, members: ReadonlyMap<string, string>): string {
	if (secret.updatedByType === USER_ENTITY)
		return members.get(secret.updatedById) ?? secret.updatedById.slice(0, SHORT_ID);
	return `${secret.updatedByType} token`;
}

/** Whether a secret matches a search over its name and the resources that use it. */
export function matchesSearch(secret: Secret, query: string): boolean {
	const q = query.trim().toLowerCase();
	if (q === "") return true;
	return (
		secret.name.toLowerCase().includes(q) || secret.usedBy.some((user) => user.resourceSlug.toLowerCase().includes(q))
	);
}
