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
	platformDomain: string,
	own: readonly string[],
	result: CheckResult,
): DomainCheck {
	const host = `${subdomain}.${platformDomain}`;
	if (subdomain === "" || platformDomain === "") return { state: "empty", host };
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
