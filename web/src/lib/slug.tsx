import { withCode, type Message } from "@/components/design/InlineCode";
const MAX_NAME_LENGTH = 63;
const MAX_SLUG_LENGTH = 63;
const COMBINING_MARK_RE = /\p{Mn}/u;
const DNS_LABEL_RE = /^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$/;
// mirrors PrivateServiceName in k8sapi/v1alpha1/discovery.go; update both together.
const RESERVED_SLUG_RE = /^(localhost|resource-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/;

export function nameError(name: string, taken: ReadonlySet<string>): Message | null {
	const trimmed = name.trim();
	if (trimmed === "") return "Enter a name";
	if (Array.from(trimmed).length > MAX_NAME_LENGTH) return `Use at most ${MAX_NAME_LENGTH.toString()} characters`;
	const lower = trimmed.toLowerCase();
	if ([...taken].some((t) => t.toLowerCase() === lower)) return withCode`${trimmed} already exists in this workspace`;
	return null;
}

// derivedSlugError reports why the slug the API derives from a new name can't key it, or null.
export function derivedSlugError(name: string, taken: ReadonlySet<string>): Message | null {
	const derived = slug(name);
	if (derived === "") return "Include a letter from a to z";
	if (!DNS_LABEL_RE.test(derived)) return "Start the name with a letter";
	if (RESERVED_SLUG_RE.test(derived)) return withCode`${name.trim()} is reserved`;
	if (taken.has(derived)) return "Too close to an existing name in this workspace";
	return null;
}

// mirrors Slug in internal/locofile/name.go; update both together.
export function slug(name: string): string {
	let out = "";
	for (const char of name.toLowerCase().normalize("NFD")) {
		if (COMBINING_MARK_RE.test(char)) continue;
		if ((char >= "a" && char <= "z") || (char >= "0" && char <= "9")) out += char;
		else if (out !== "" && !out.endsWith("-")) out += "-";
		if (out.length >= MAX_SLUG_LENGTH) break;
	}
	return out.replace(/^-+|-+$/g, "");
}
