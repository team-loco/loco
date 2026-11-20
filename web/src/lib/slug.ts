const MAX_SLUG_LENGTH = 63;
const COMBINING_MARK_RE = /\p{Mn}/u;

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
