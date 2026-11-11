import { createContext, use, type ReactNode } from "react";
import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { useMyScopes } from "@/hooks/useMyScopes";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import type { Paging } from "@/lib/paging";
import type { ObservabilityView } from "@/lib/routes";
import { configuredRegions } from "@/lib/resource-regions";

import { liveTokens, narrow, readQuery, writeQuery, type Token } from "./query";
import { EVENTS_MAX_MS, QUERY_MAX_MS, readWindow, writeWindow, type TimeWindow } from "@/lib/obs-time";

const SERIES_COLORS = 8;
const VIEW_STATE_KEYS = ["page", "at", "trace", "span"];

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
	params: URLSearchParams;
	resources: ObsResource[];
	resourcesLoading: boolean;
	resourceByName: Map<string, ObsResource>;
	resourceById: Map<string, ObsResource>;
	maxMs: number;
	range: TimeWindow;
	setRange: (w: TimeWindow) => void;
	tokens: Token[];
	text: string;
	setQuery: (tokens: Token[], text: string) => void;
	setParam: (key: string, value: string | null, replace?: boolean) => void;
	setParams: (next: URLSearchParams, replace?: boolean) => void;
	setPaging: (paging: Paging, defaultSize: number) => void;
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
	const [params, setParams] = useSearchParams();
	const grants = useMyScopes().resourceGrants(orgId, workspaceId);
	const { data: resourcesData, isLoading: resourcesLoading } = useQuery(
		workspaceResourcesOptions(transport, workspaceId, grants),
	);

	const maxMs = maxSpanFor(view);
	const range = readWindow(params, maxMs);
	const { tokens, text } = readQuery(params);

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

	const commit = (next: URLSearchParams, replace = false) => {
		setParams(next, { replace });
	};
	const resetViewState = (next: URLSearchParams) => {
		for (const key of VIEW_STATE_KEYS) next.delete(key);
		return next;
	};

	const setRange = (w: TimeWindow) => {
		commit(resetViewState(writeWindow(params, w)));
	};
	const setQuery = (nextTokens: Token[], nextText: string) => {
		const next = writeQuery(params, nextTokens, nextText);
		next.delete("page");
		commit(next);
	};
	const setParam = (key: string, value: string | null, replace = false) => {
		const next = new URLSearchParams(params);
		if (value === null) next.delete(key);
		else next.set(key, value);
		commit(next, replace);
	};
	const setPaging = ({ page, size }: Paging, defaultSize: number) => {
		const next = new URLSearchParams(params);
		if (page === 0) next.delete("page");
		else next.set("page", String(page + 1));
		if (size === defaultSize) next.delete("size");
		else next.set("size", String(size));
		commit(next, true);
	};

	const goTo = (next: ObservabilityView, opts: GoToOptions = {}) => {
		const search = new URLSearchParams({ view: next });
		const env = params.get("env");
		if (env !== null) search.set("env", env);
		const target = writeQuery(writeWindow(search, opts.range ?? range), opts.tokens ?? tokens, opts.tokens ? "" : text);
		if (opts.focusTs !== undefined) target.set("at", String(opts.focusTs));
		if (opts.trace !== undefined) target.set("trace", opts.trace);
		if (opts.span !== undefined) target.set("span", opts.span);
		commit(target);
	};

	const value: ObsContextValue = {
		orgId,
		workspaceId,
		view,
		params,
		resources,
		resourcesLoading,
		resourceByName,
		resourceById,
		maxMs,
		range,
		setRange,
		tokens,
		text,
		setQuery,
		setParam,
		setParams: commit,
		setPaging,
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
