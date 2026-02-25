import {
	createContext,
	use,
	useMemo,
	useState,
	type ReactNode,
} from "react";
import type { Transport } from "@connectrpc/connect";
import { useObsAccess } from "@/hooks/useObsAccess";
import { createObsTransport } from "@/lib/obs-transport";
import type { Resource } from "@/gen/loco/resource/v1/resource_pb";
import type { ClusterAccess } from "@/gen/loco/observability/v1/observability_access_pb";

export type TimeRange = "15m" | "1h" | "3h" | "6h" | "24h" | "7d";

export interface ClusterTransport {
	cluster: ClusterAccess;
	transport: Transport;
}

interface ObsContextType {
	workspaceId: string;
	resources: Resource[];
	// Token + cluster access
	token: string;
	clusters: ClusterAccess[];
	clusterTransports: ClusterTransport[];
	isLoading: boolean;
	error: Error | null;
	// Filters
	selectedResourceIds: string[];
	setSelectedResourceIds: (ids: string[]) => void;
	activeClusterIds: string[];
	setActiveClusterIds: (ids: string[]) => void;
	timeRange: TimeRange;
	setTimeRange: (range: TimeRange) => void;
}

const ObsContext = createContext<ObsContextType | null>(null);

export function ObsProvider({
	workspaceId,
	resources,
	children,
}: {
	workspaceId: string;
	resources: Resource[];
	children: ReactNode;
}) {
	const { data: access, isLoading, error } = useObsAccess(workspaceId);

	const [selectedResourceIds, setSelectedResourceIds] = useState<string[]>([]);
	const [activeClusterIds, setActiveClusterIds] = useState<string[]>([]);
	const [timeRange, setTimeRange] = useState<TimeRange>("1h");

	const token = access?.token ?? "";
	const clusters = useMemo(() => access?.clusters ?? [], [access]);

	const clusterTransports = useMemo((): ClusterTransport[] => {
		if (!token || clusters.length === 0) return [];
		return clusters
			.filter(
				(c) =>
					activeClusterIds.length === 0 ||
					activeClusterIds.includes(c.clusterId.toString()),
			)
			.map((cluster) => ({
				cluster,
				transport: createObsTransport(cluster.proxyUrl, token),
			}));
	}, [token, clusters, activeClusterIds]);

	return (
		<ObsContext
			value={{
				workspaceId,
				resources,
				token,
				clusters,
				clusterTransports,
				isLoading,
				error: error as Error | null,
				selectedResourceIds,
				setSelectedResourceIds,
				activeClusterIds,
				setActiveClusterIds,
				timeRange,
				setTimeRange,
			}}
		>
			{children}
		</ObsContext>
	);
}

// eslint-disable-next-line react-refresh/only-export-components
export function useObs() {
	const ctx = use(ObsContext);
	if (!ctx) throw new Error("useObs must be used within ObsProvider");
	return ctx;
}

// eslint-disable-next-line react-refresh/only-export-components
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

// eslint-disable-next-line react-refresh/only-export-components
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
