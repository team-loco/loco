export function resolveAPIURL(runtimeURL: string | undefined, buildURL: string, origin: string): string {
	const configured = runtimeURL ?? buildURL;
	return new URL(configured === "" ? origin : configured, origin).href.replace(/\/$/, "");
}
