const MAX_NAME_LENGTH = 63;
const MAX_SLUG_LENGTH = 63;
const COMBINING_MARK_RE = /\p{Mn}/u;
const DNS_LABEL_RE = /^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$/;
const DIGIT_LEAD_RE = /^[0-9]/;
// mirrors PrivateServiceName in k8sapi/v1alpha1/discovery.go; update both together.
const RESERVED_SLUG_RE = /^(localhost|resource-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/;
export const SLUG_RULE =
	"Use 1 to 63 lowercase letters, digits and hyphens, starting with a letter and ending with a letter or digit";
export const SLUG_DIGIT_LEAD = "Start the slug with a letter";

export function nameError(name: string, taken: ReadonlySet<string>): string | null {
	const trimmed = name.trim();
	if (trimmed === "") return "Enter a name";
	if (Array.from(trimmed).length > MAX_NAME_LENGTH) return `Use at most ${MAX_NAME_LENGTH.toString()} characters`;
	const lower = trimmed.toLowerCase();
	if ([...taken].some((t) => t.toLowerCase() === lower)) return `${trimmed} already exists in this workspace`;
	return null;
}

export function slugError(value: string, taken: ReadonlySet<string>): string | null {
	if (value === "") return "Enter a slug";
	if (!DNS_LABEL_RE.test(value))
		return DIGIT_LEAD_RE.test(value) && DNS_LABEL_RE.test(`a${value}`) ? SLUG_DIGIT_LEAD : SLUG_RULE;
	if (RESERVED_SLUG_RE.test(value)) return `${value} is reserved`;
	if (taken.has(value)) return `${value} already exists in this workspace`;
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
