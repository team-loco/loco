import type { Secret } from "@gen/loco/secret/v1/secret_pb";

import { SECRET_NAME } from "./settings-draft";

const USER_ENTITY = "user";
const SHORT_ID = 8;

export type SecretStatus = "set" | "missing" | "unused" | "new";

export interface SecretRow {
	name: string;
	secret: Secret | undefined;
	status: SecretStatus | undefined;
}

function rowStatus(stored: boolean, declared: ReadonlySet<string> | undefined, name: string): SecretStatus | undefined {
	if (declared === undefined) return undefined;
	if (!declared.has(name)) return "unused";
	if (stored) return "set";
	return "missing";
}

export function secretRows(secrets: Secret[], declaredNames: string[] | undefined, staged: string[]): SecretRow[] {
	const stored = new Map(secrets.map((s) => [s.name, s]));
	const declared = declaredNames === undefined ? undefined : new Set(declaredNames);
	const names = new Set([...stored.keys(), ...(declaredNames ?? []), ...staged]);
	return [...names]
		.toSorted((a, b) => a.localeCompare(b))
		.map((name) => {
			const secret = stored.get(name);
			const known = secret !== undefined || declared?.has(name) === true;
			const status = known ? rowStatus(secret !== undefined, declared, name) : "new";
			return { name, secret, status };
		});
}

export function setterLabel(secret: Secret, members: ReadonlyMap<string, string>): string {
	if (secret.updatedByType === USER_ENTITY)
		return members.get(secret.updatedById) ?? secret.updatedById.slice(0, SHORT_ID);
	return `${secret.updatedByType} token`;
}

export function secretNameProblem(name: string): string | undefined {
	if (name === "") return "Enter a name.";
	if (!SECRET_NAME.test(name)) return "Use capital letters, digits and underscores, not starting with a digit.";
	return undefined;
}
