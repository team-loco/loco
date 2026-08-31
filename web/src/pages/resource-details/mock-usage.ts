import type { StatusKey } from "./status";

// PLACEHOLDER DATA — the per-region CPU/memory bars on this page are drawn from
// this function, not from telemetry. Deterministic noise seeded off the status
// and row index. Delete once the obs pipeline feeds the region cards for real.
export function mockUsagePct(statusKey: StatusKey, seed: number): number {
	const BASES = [23, 31, 17, 41, 29, 37, 13, 43] as const;
	const base = BASES[seed % BASES.length] ?? BASES[0];
	if (statusKey === "degraded") return 72 + (base % 18);
	if (statusKey === "failed") return 88 + (base % 10);
	return 28 + (base % 38);
}
