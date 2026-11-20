const MAX_SLUG_LENGTH = 63;
const COMBINING_MARK_RE = /\p{Mn}/u;
export const DNS_LABEL_RE = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/;
export const SLUG_RULE =
	"Use 1 to 63 lowercase letters, digits and hyphens, starting and ending with a letter or digit";

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
