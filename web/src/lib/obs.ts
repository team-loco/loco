import type { Transport } from "@connectrpc/connect";
import type { ClusterAccess } from "@gen/loco/observability/v1/observability_access_pb";

export type TimeRange = "15m" | "1h" | "3h" | "6h" | "24h" | "7d";

export interface ClusterTransport {
	cluster: ClusterAccess;
	transport: Transport;
}

export function timeRangeMs(range: TimeRange): number {
	const map: Record<TimeRange, number> = {
		"15m": 15 * 60 * 1000,
		"1h": 60 * 60 * 1000,
		"3h": 3 * 60 * 60 * 1000,
		"6h": 6 * 60 * 60 * 1000,
		"24h": 24 * 60 * 60 * 1000,
		"7d": 7 * 24 * 60 * 60 * 1000,
	};
	return map[range];
}

export function timeRangeIntervalSeconds(range: TimeRange): number {
	const map: Record<TimeRange, number> = {
		"15m": 30,
		"1h": 60,
		"3h": 180,
		"6h": 300,
		"24h": 900,
		"7d": 3600,
	};
	return map[range];
}
