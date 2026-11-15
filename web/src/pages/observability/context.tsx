import { createContext, use, type ReactNode } from "react";
import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { parseAsString, useQueryStates } from "nuqs";
import { useNavigate } from "react-router";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { useMyScopes } from "@/hooks/useMyScopes";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { observabilityPath, type ObservabilityView } from "@/lib/routes";
import { configuredRegions } from "@/lib/resource-regions";

import { liveTokens, narrow, QUERY_PARSERS, queryStr, queryValues, toQuery, type Token } from "./query";
import { EVENTS_MAX_MS, QUERY_MAX_MS, toWindow, WINDOW_PARSERS, windowValues, type TimeWindow } from "@/lib/obs-time";

const SERIES_COLORS = 8;

// The views read page, at, trace, span and log with their own parsers; the provider only clears them.
const OBS_PARAMS = {
	...WINDOW_PARSERS,
	...QUERY_PARSERS,
	env: parseAsString,
	page: parseAsString,
	at: parseAsString,
	trace: parseAsString,
	span: parseAsString,
	log: parseAsString,
};
const CLEARED_VIEW_STATE = { page: null, at: null, trace: null, span: null, log: null };

export function seriesColor(index: number): string {
	return `var(--series-${String(index % SERIES_COLORS)})`;
}

export interface ObsResource {
	id: string;
	name: string;
	color: string;
	regions: string[];
	raw: Resource;
}

export interface GoToOptions {
	range?: TimeWindow | undefined;
	tokens?: Token[] | undefined;
	focusTs?: number | undefined;
	trace?: string | undefined;
	span?: string | undefined;
}

interface ObsContextValue {
	orgId: string;
	workspaceId: string;
	view: ObservabilityView;
	resources: ObsResource[];
	resourcesLoading: boolean;
	/** IDs every query must name because the user reads only these resources, or null when it may query the whole workspace. */
	scopedResourceIds: string[] | null;
	resourceByName: Map<string, ObsResource>;
	resourceById: Map<string, ObsResource>;
	maxMs: number;
	range: TimeWindow;
	setRange: (w: TimeWindow) => void;
	tokens: Token[];
	text: string;
	setQuery: (tokens: Token[], text: string) => void;
	goTo: (view: ObservabilityView, opts?: GoToOptions) => void;
}

const ObsContext = createContext<ObsContextValue | null>(null);

export function maxSpanFor(view: ObservabilityView): number {
	return view === "events" ? EVENTS_MAX_MS : QUERY_MAX_MS;
}

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
	const transport = useTransport();
	const navigate = useNavigate();
	const [params, setParams] = useQueryStates(OBS_PARAMS);
	const grants = useMyScopes().resourceGrants(orgId, workspaceId);
	const { data: resourcesData, isLoading: resourcesLoading } = useQuery(
		workspaceResourcesOptions(transport, workspaceId, grants),
	);

	const maxMs = maxSpanFor(view);
	const range = toWindow(params, maxMs);
	const { tokens, text } = toQuery(params);

	const sorted = (resourcesData?.resources ?? []).toSorted((a, b) => a.name.localeCompare(b.name));
	const resources: ObsResource[] = sorted.map((r, i) => ({
		id: r.id,
		name: r.name,
		color: seriesColor(i),
		regions: configuredRegions(r),
		raw: r,
	}));
	const resourceByName = new Map(resources.map((r) => [r.name, r]));
	const resourceById = new Map(resources.map((r) => [r.id, r]));
	const scopedResourceIds = grants === "workspace" ? null : resources.map((r) => r.id);

	const setRange = (w: TimeWindow) => {
		void setParams({ ...windowValues(w), ...CLEARED_VIEW_STATE });
	};
	const setQuery = (nextTokens: Token[], nextText: string) => {
		void setParams({ ...queryValues(nextTokens, nextText), page: null, log: null });
	};

	const goTo = (next: ObservabilityView, opts: GoToOptions = {}) => {
		void navigate(
			observabilityPath(orgId, workspaceId, next, {
				env: params.env,
				...windowValues(opts.range ?? range),
				q: queryStr(opts.tokens ?? tokens, opts.tokens ? "" : text) || null,
				at: opts.focusTs ?? null,
				trace: opts.trace ?? null,
				span: opts.span ?? null,
			}),
		);
	};

	const value: ObsContextValue = {
		orgId,
		workspaceId,
		view,
		resources,
		resourcesLoading,
		scopedResourceIds,
		resourceByName,
		resourceById,
		maxMs,
		range,
		setRange,
		tokens,
		text,
		setQuery,
		goTo,
	};

	return <ObsContext value={value}>{children}</ObsContext>;
}

export function useObs(): ObsContextValue {
	const ctx = use(ObsContext);
	if (!ctx) throw new Error("useObs must be used within ObsProvider");
	return ctx;
}

export function filteredResources(view: ObservabilityView, all: ObsResource[], tokens: Token[]): ObsResource[] {
	const names = narrow(
		all.map((r) => r.name),
		liveTokens(view, tokens, "resource"),
	);
	if (names === null) return all;
	const set = new Set(names);
	return all.filter((r) => set.has(r.name));
}
