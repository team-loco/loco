import { createContext, use, type ReactNode } from "react";
import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { parseAsString, useQueryStates } from "nuqs";
import { useNavigate } from "react-router";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { useObservabilityMaxRange } from "@/hooks/useObservabilityMaxRange";
import { Skeleton } from "@/components/design/Skeleton";
import { useMyScopes } from "@/hooks/useMyScopes";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { useEnvironments } from "@/hooks/useEnvironment";
import {
	observabilityPath,
	systemObservabilityPath,
	type ObservabilityView,
	type SystemObservabilityView,
} from "@/lib/routes";
import { configuredRegions } from "@/lib/resource-regions";

import { liveTokens, narrow, QUERY_PARSERS, queryStr, queryValues, toQuery, type Token } from "./query";
import { EVENTS_MAX_MS, toWindow, WINDOW_PARSERS, windowValues, type TimeWindow } from "@/lib/obs-time";

const SERIES_COLORS = 8;

// The views read page, at, trace, span, log and event with their own parsers; the provider only clears them.
const OBS_PARAMS = {
	...WINDOW_PARSERS,
	...QUERY_PARSERS,
	env: parseAsString,
	page: parseAsString,
	at: parseAsString,
	trace: parseAsString,
	span: parseAsString,
	log: parseAsString,
	event: parseAsString,
};
const CLEARED_VIEW_STATE = { page: null, at: null, trace: null, span: null, log: null, event: null };

export function seriesColor(index: number): string {
	return `var(--series-${String(index % SERIES_COLORS)})`;
}

export interface ObsResource {
	id: string;
	slug: string;
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
	/** Empty in the installation scope. */
	orgId: string;
	/** Empty in the installation scope. */
	workspaceId: string;
	/** Reads Loco's own telemetry, which has no workspace, environment or resources. */
	installation: boolean;
	/** The environment queries name; empty when the workspace has none yet or in the installation scope. */
	environmentId: string;
	environmentLoading: boolean;
	view: ObservabilityView;
	resources: ObsResource[];
	resourcesLoading: boolean;
	/** True once the workspace's resources have loaded and a reader of the whole workspace sees none. */
	noResources: boolean;
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

/** The range and query every observability page keeps in the URL, or undefined while the maximum range loads. */
function useObsParams(view: ObservabilityView) {
	const [params, setParams] = useQueryStates(OBS_PARAMS);
	const queryMaxMs = useObservabilityMaxRange();
	if (queryMaxMs === undefined) return undefined;
	const maxMs = view === "events" ? EVENTS_MAX_MS : queryMaxMs;
	const range = toWindow(params, maxMs);
	const { tokens, text } = toQuery(params);
	return {
		params,
		maxMs,
		range,
		tokens,
		text,
		/** The range, query, focus and trace a jump to another view carries. */
		link: (opts: GoToOptions) => ({
			...windowValues(opts.range ?? range),
			q: queryStr(opts.tokens ?? tokens, opts.tokens ? "" : text) || null,
			at: opts.focusTs ?? null,
			trace: opts.trace ?? null,
			span: opts.span ?? null,
		}),
		setRange: (w: TimeWindow) => {
			void setParams({ ...windowValues(w), ...CLEARED_VIEW_STATE });
		},
		setQuery: (nextTokens: Token[], nextText: string) => {
			void setParams({ ...queryValues(nextTokens, nextText), page: null, log: null, event: null });
		},
	};
}

const NO_RESOURCES = {
	resources: [],
	resourceByName: new Map<string, ObsResource>(),
	resourceById: new Map<string, ObsResource>(),
};

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
	const shared = useObsParams(view);
	const grants = useMyScopes().resourceGrants(orgId, workspaceId);
	const { data: resourcesData, isLoading: resourcesLoading } = useQuery({
		...workspaceResourcesOptions(transport, workspaceId, grants),
		enabled: view !== "metrics",
	});
	const { active: environment, isLoading: environmentLoading } = useEnvironments();
	if (shared === undefined) return <Skeleton className="h-64 w-full" />;
	const { params, maxMs, range, tokens, text, link, setRange, setQuery } = shared;

	const sourceResources = view === "metrics" ? [] : (resourcesData?.resources ?? []);
	const sorted = sourceResources.toSorted((a, b) => a.slug.localeCompare(b.slug));
	const resources: ObsResource[] = sorted.map((r, i) => ({
		id: r.id,
		slug: r.slug,
		color: seriesColor(i),
		regions: configuredRegions(r),
		raw: r,
	}));
	const resourceByName = new Map(resources.map((r) => [r.slug, r]));
	const resourceById = new Map(resources.map((r) => [r.id, r]));
	const scopedResourceIds = grants === "workspace" ? null : resources.map((r) => r.id);

	const goTo = (next: ObservabilityView, opts: GoToOptions = {}) => {
		void navigate(observabilityPath(orgId, workspaceId, next, { env: params.env, ...link(opts) }));
	};

	const noResources = view !== "metrics" && grants === "workspace" && !resourcesLoading && resources.length === 0;

	const value: ObsContextValue = {
		orgId,
		workspaceId,
		installation: false,
		environmentId: environment?.id ?? "",
		environmentLoading,
		view,
		resources,
		resourcesLoading: view === "metrics" ? grants === undefined : resourcesLoading,
		noResources,
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

/** The provider for Loco's own telemetry: no workspace, environment or resources. */
export function InstallationObsProvider({ view, children }: { view: SystemObservabilityView; children: ReactNode }) {
	const navigate = useNavigate();
	const shared = useObsParams(view);
	if (shared === undefined) return <Skeleton className="h-64 w-full" />;
	const { maxMs, range, tokens, text, link, setRange, setQuery } = shared;

	const goTo = (next: ObservabilityView, opts: GoToOptions = {}) => {
		if (next === "events") return;
		void navigate(systemObservabilityPath(next, link(opts)));
	};

	const value: ObsContextValue = {
		orgId: "",
		workspaceId: "",
		installation: true,
		environmentId: "",
		environmentLoading: false,
		view,
		...NO_RESOURCES,
		resourcesLoading: false,
		noResources: false,
		scopedResourceIds: null,
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
		all.map((r) => r.slug),
		liveTokens(view, tokens, "resource"),
	);
	if (names === null) return all;
	const set = new Set(names);
	return all.filter((r) => set.has(r.slug));
}
