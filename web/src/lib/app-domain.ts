export const SUBDOMAIN_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
export const MAX_SUBDOMAIN_LENGTH = 63;

export type DomainCheck =
	| { state: "empty" | "long" | "invalid" | "mine" | "checking" | "available" | "taken"; host: string }
	| { state: "failed"; host: string; error: Error };

export interface CheckResult {
	fetching: boolean;
	available: boolean | undefined;
	error: Error | null;
}

export function domainCheck(
	subdomain: string,
	appDomain: string,
	own: readonly string[],
	result: CheckResult,
): DomainCheck {
	const host = `${subdomain}.${appDomain}`;
	if (subdomain === "" || appDomain === "") return { state: "empty", host };
	if (subdomain.length > MAX_SUBDOMAIN_LENGTH) return { state: "long", host };
	if (!SUBDOMAIN_RE.test(subdomain)) return { state: "invalid", host };
	if (own.includes(host)) return { state: "mine", host };
	if (result.error !== null) return { state: "failed", host, error: result.error };
	if (result.fetching || result.available === undefined) return { state: "checking", host };
	return { state: result.available ? "available" : "taken", host };
}

export function domainCoverage(served: readonly string[], running: readonly string[]) {
	return {
		serving: served.filter((r) => running.includes(r)),
		missing: running.filter((r) => !served.includes(r)),
	};
}

/** The hostname a region's gateway also answers for `hostname` when it is one label under `appDomain`, else null. */
export function regionalHostname(hostname: string, appDomain: string, region: string): string | null {
	const dot = hostname.indexOf(".");
	if (dot < 1 || hostname.slice(dot + 1) !== appDomain) return null;
	return `${hostname.slice(0, dot)}.${region}.${appDomain}`;
}
