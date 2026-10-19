const SHORT_DIGEST_HEX = 12;
const SHORT_REPOSITORY_LENGTH = 24;
const SHORT_TAG_LENGTH = 16;
const SHORT_SUFFIX_LENGTH = 6;
const IMPLICIT_TAG = "latest";

interface ImageParts {
	repository: string;
	tag: string;
	digest: string;
}

export function parseImage(image: string): ImageParts {
	const at = image.indexOf("@");
	const name = at === -1 ? image : image.slice(0, at);
	const digest = at === -1 ? "" : image.slice(at + 1);
	const slash = name.lastIndexOf("/");
	const colon = name.lastIndexOf(":");
	const hasTag = colon > slash;
	const path = hasTag ? name.slice(0, colon) : name;
	const repository = path.slice(path.lastIndexOf("/") + 1);
	const tag = hasTag ? name.slice(colon + 1) : "";
	return { repository, tag, digest };
}

export function shortDigest(digest: string): string {
	const colon = digest.indexOf(":");
	return digest.slice(0, colon + 1 + SHORT_DIGEST_HEX);
}

export function imageVersion(image: string): string {
	const { tag, digest } = parseImage(image);
	if (tag !== "") return tag;
	if (digest !== "") return shortDigest(digest);
	return IMPLICIT_TAG;
}

function compactImagePart(part: string, limit: number): string {
	if (part.length <= limit) return part;
	return `${part.slice(0, limit - SHORT_SUFFIX_LENGTH - 1)}…${part.slice(-SHORT_SUFFIX_LENGTH)}`;
}

export function shortImageRef(image: string): string {
	const { repository, tag, digest } = parseImage(image);
	const shortRepository = compactImagePart(repository, SHORT_REPOSITORY_LENGTH);
	if (tag !== "") return `${shortRepository}:${compactImagePart(tag, SHORT_TAG_LENGTH)}`;
	if (digest !== "") return `${shortRepository}@${shortDigest(digest)}`;
	return shortRepository;
}
