import { createContext, use, useState, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { useSearchParams } from "react-router";
import type { ClusterAccess } from "@gen/loco/observability/v1/observability_access_pb";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { createTransport } from "@/auth/connect-transport";
import { useObsAccess } from "@/hooks/useObsAccess";
import type { ClusterTransport, TimeRange } from "@/lib/obs";
import type { ObservabilityView } from "@/lib/routes";

import type { Token } from "./query";

export const RESOURCE_COLORS = ["#1e40af", "#0e7490", "#7c3aed", "#b45309", "#be185d", "#15803d", "#4f46e5", "#a16207"];

export interface ObsResource {
	id: string;
	name: string;
	color: string;
	region: string;
	raw: Resource;
}

export const RANGES: { key: TimeRange; label: string }[] = [
	{ key: "15m", label: "Last 15 minutes" },
	{ key: "1h", label: "Last hour" },
	{ key: "6h", label: "Last 6 hours" },
	{ key: "24h", label: "Last 24 hours" },
	{ key: "7d", label: "Last 7 days" },
];

const RANGE_SECONDS: Record<string, number> = { "15m": 900, "1h": 3600, "6h": 21600, "24h": 86400, "7d": 604800 };

export function fitRange(ts: number, now: number, max: TimeRange = "24h"): TimeRange {
	const ago = (now - ts) / 1000 + 300;
	const fit = RANGES.find((r) => (RANGE_SECONDS[r.key] ?? 0) >= ago);
	if (fit === undefined) return max;
	return (RANGE_SECONDS[fit.key] ?? 0) > (RANGE_SECONDS[max] ?? 0) ? max : fit.key;
}

function isTimeRange(v: string | null): v is TimeRange {
	return v === "15m" || v === "1h" || v === "3h" || v === "6h" || v === "24h" || v === "7d";
}

interface GoToOptions {
	range?: TimeRange | undefined;
	resource?: string | undefined;
	tokens?: Token[] | undefined;
	focusTs?: number | undefined;
}

interface ObsContextValue {
	orgId: string;
	workspaceId: string;
	view: ObservabilityView;
	resources: ObsResource[];
	resourcesLoading: boolean;
	resourceByName: Map<string, ObsResource>;
	resourceById: Map<string, ObsResource>;
	clusters: ClusterAccess[];
	transports: ClusterTransport[];
	accessLoading: boolean;
	accessError: Error | null;
	range: TimeRange;
	setRange: (r: TimeRange) => void;
	selected: string[] | null;
	setSelected: (names: string[] | null) => void;
	tokens: Token[];
	setTokens: (t: Token[]) => void;
	text: string;
	setText: (t: string) => void;
	appliedText: string;
	setAppliedText: (t: string) => void;
	logFocusTs: number | null;
	setLogFocusTs: (ts: number | null) => void;
	metricsFocusTs: number | null;
	goTo: (view: ObservabilityView, opts?: GoToOptions) => void;
}

const ObsContext = createContext<ObsContextValue | null>(null);

export function ObsProvider({
	orgId,
	workspaceId,
	view,
	children,
}: {
	orgId: string;
	workspaceId: string;
	view: ObservabilityView;
	children: ReactNode;
}) {
	const [params, setParams] = useSearchParams();
	const access = useObsAccess(workspaceId);
	const { data: resourcesData, isLoading: resourcesLoading } = useQuery(
		listWorkspaceResources,
		{ workspaceId, pageSize: 200 },
		{ enabled: workspaceId !== "" },
	);

	const initialNames = (params.get("r") ?? "").split(",").filter(Boolean);
	const initialRange = params.get("range");
	const [range, setRange] = useState<TimeRange>(isTimeRange(initialRange) ? initialRange : "1h");
	const [selected, setSelected] = useState<string[] | null>(initialNames.length > 0 ? initialNames : null);
	const [tokens, setTokens] = useState<Token[]>(() =>
		initialNames.map((value) => ({ neg: false, key: "resource", value })),
	);
	const [text, setText] = useState("");
	const [appliedText, setAppliedText] = useState("");
	const [logFocusTs, setLogFocusTs] = useState<number | null>(null);
	const [metricsFocusTs, setMetricsFocusTs] = useState<number | null>(null);

	const sorted = (resourcesData?.resources ?? []).toSorted((a, b) => a.name.localeCompare(b.name));
	const resources: ObsResource[] = sorted.map((r, i) => {
		const primary = r.regions.find((g) => g.isPrimary) ?? r.regions[0];
		return {
			id: r.id,
			name: r.name,
			color: RESOURCE_COLORS[i % RESOURCE_COLORS.length] ?? "#1e40af",
			region: primary?.region ?? "",
			raw: r,
		};
	});
	const resourceByName = new Map(resources.map((r) => [r.name, r]));
	const resourceById = new Map(resources.map((r) => [r.id, r]));

	const clusters = access.data?.clusters ?? [];
	const [transportCache] = useState(() => new Map<string, ClusterTransport>());
	const transports = clusters.map((cluster) => {
		const key = `${cluster.clusterId}|${cluster.proxyUrl}`;
		const cached = transportCache.get(key);
		if (cached !== undefined) return cached;
		const created = { cluster, transport: createTransport(cluster.proxyUrl) };
		transportCache.set(key, created);
		return created;
	});

	const goTo = (next: ObservabilityView, opts: GoToOptions = {}) => {
		if (opts.range !== undefined) setRange(opts.range);
		if (next === "logs") {
			if (opts.tokens !== undefined) {
				setTokens(opts.tokens);
				setText("");
				setAppliedText("");
			} else if (opts.resource !== undefined) {
				setTokens([{ neg: false, key: "resource", value: opts.resource }]);
				setText("");
				setAppliedText("");
			}
			setLogFocusTs(opts.focusTs ?? null);
		}
		if (next === "metrics") {
			if (opts.resource !== undefined) setSelected([opts.resource]);
			setMetricsFocusTs(opts.focusTs ?? null);
		}
		if (next === "events" && opts.resource !== undefined) setSelected([opts.resource]);
		const env = params.get("env");
		const search = new URLSearchParams({ view: next });
		if (env !== null) search.set("env", env);
		setParams(search);
	};

	const value: ObsContextValue = {
		orgId,
		workspaceId,
		view,
		resources,
		resourcesLoading,
		resourceByName,
		resourceById,
		clusters,
		transports,
		accessLoading: access.isLoading,
		accessError: access.error,
		range,
		setRange,
		selected,
		setSelected,
		tokens,
		setTokens,
		text,
		setText,
		appliedText,
		setAppliedText,
		logFocusTs,
		setLogFocusTs,
		metricsFocusTs,
		goTo,
	};

	return <ObsContext value={value}>{children}</ObsContext>;
}

export function useObs(): ObsContextValue {
	const ctx = use(ObsContext);
	if (!ctx) throw new Error("useObs must be used within ObsProvider");
	return ctx;
}

export function selectedResources(all: ObsResource[], selected: string[] | null): ObsResource[] {
	if (selected === null) return all;
	const set = new Set(selected);
	const picked = all.filter((r) => set.has(r.name));
	return picked.length > 0 ? picked : all;
}
