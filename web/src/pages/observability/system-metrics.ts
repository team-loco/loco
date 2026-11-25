import { trafficValue } from "@/lib/traffic";

import { liveTokens, passes, type Token } from "./query";

export interface SystemChart {
	key: string;
	title: string;
	metric: string;
	aggregation: "avg" | "max" | "rate";
	/** The unit the axis shows, with the space that separates it from the number. */
	unit: string;
	format: (value: number) => string;
}

export interface SystemGroup {
	key: string;
	title: string;
	/** The sender's service name, or null for the worker clusters' collectors, which name none. */
	component: string | null;
	charts: SystemChart[];
}

const perSecond = (key: string, title: string, metric: string): SystemChart => ({
	key,
	title,
	metric,
	aggregation: "rate",
	unit: "/s",
	format: (value) => trafficValue(value, "/s"),
});

const peak = (key: string, title: string, metric: string, unit: string): SystemChart => ({
	key,
	title,
	metric,
	aggregation: "max",
	unit,
	format: (value) => trafficValue(value, unit),
});

const GOROUTINES = peak("goroutines", "Goroutines", "go.goroutine.count", " goroutines");

function controller(key: string, title: string, component: string): SystemGroup {
	return {
		key,
		title,
		component,
		charts: [
			perSecond("reconciles", "Reconciles", "controller_runtime_reconcile_total"),
			perSecond("reconcile-errors", "Reconcile errors", "controller_runtime_reconcile_errors_total"),
			peak("queue", "Work queue depth", "workqueue_depth", " items"),
			GOROUTINES,
		],
	};
}

/** The metrics Loco's components export, by the component that sends them. */
export const SYSTEM_GROUPS: SystemGroup[] = [
	{
		key: "api",
		title: "API",
		component: "loco-api",
		charts: [
			peak("streams", "Open streams", "loco.api.streams.open", " streams"),
			perSecond("refused", "Refused streams", "loco.api.streams.refused"),
			GOROUTINES,
		],
	},
	controller("controller", "Application controller", "loco-controller"),
	controller("build-controller", "Build controller", "loco-build-controller"),
	{
		key: "clusters",
		title: "Worker clusters",
		component: null,
		charts: [
			peak("node-cpu", "Node CPU in use", "k8s.node.cpu.usage", " cores"),
			peak("node-allocatable", "Node CPU allocatable", "k8s.node.allocatable_cpu", " cores"),
		],
	},
];

/** The groups the @component terms of a metrics query leave; the collectors' group has no component to name. */
export function visibleGroups(tokens: Token[]): SystemGroup[] {
	const components = liveTokens("metrics", tokens, "component");
	return SYSTEM_GROUPS.filter((g) =>
		g.component === null ? components.length === 0 : passes([g.component], components),
	);
}

export interface SystemSlice {
	name: string;
	attributes: Record<string, string>;
	region: string;
}

/** The series each chart draws: one per @cluster and @region value named, or one across everything. */
export function slices(tokens: Token[]): SystemSlice[] {
	const clusters = liveTokens("metrics", tokens, "cluster").filter((t) => !t.neg);
	const regions = liveTokens("metrics", tokens, "region").filter((t) => !t.neg);
	if (clusters.length === 0 && regions.length === 0) return [{ name: "All clusters", attributes: {}, region: "" }];
	return (clusters.length === 0 ? [undefined] : clusters).flatMap((cluster) =>
		(regions.length === 0 ? [undefined] : regions).map((region) => ({
			name: [cluster?.value, region?.value].filter((part) => part !== undefined).join(" · "),
			attributes: cluster === undefined ? {} : { cluster: cluster.value },
			region: region?.value ?? "",
		})),
	);
}
