export const DEFAULT_DOCS_URL = "https://docs.loco.build";

/** The documentation base address without a trailing slash; an empty or missing runtime value falls back to the default. */
export function resolveDocsURL(runtimeURL: string | undefined): string {
	const configured = runtimeURL === undefined || runtimeURL === "" ? DEFAULT_DOCS_URL : runtimeURL;
	return configured.replace(/\/+$/, "");
}
