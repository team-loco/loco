import type { ColumnSort, ColumnVisibilityState } from "@tanstack/react-table";
import { createParser, parseAsNumberLiteral, parseAsString, parseAsStringLiteral } from "nuqs";
import type { Deployment, ServiceDeploymentSpec } from "@gen/loco/deployment/v1/deployment_pb";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { ResourceStatus, type Resource } from "@gen/loco/resource/v1/resource_pb";

import { deploymentImage, getServiceSpec } from "@/lib/deployment-utils";
import { DRAFT_RANK, draftSizing, type Draft, type ServiceSizing } from "@/lib/drafts";
import { imageName, imageVersion } from "@/lib/image";
import { parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { configuredRegions } from "@/lib/resource-regions";
import { environmentStatus, primaryDomain, statusRank } from "@/lib/resource-status";
import { pageParser } from "@/lib/paging";
import { sortParser } from "@/lib/sort-parser";
import { tsMs } from "@/lib/time";

export const ALL = "all";
export const NOT_DEPLOYED = "not-deployed";
export const PAGE_SIZES = [25, 50, 100] as const;

export const STATUS_FILTERS: {
	value: string;
	label: string;
	status?: ResourceStatus;
}[] = [
	{ value: "degraded", label: "Degraded", status: ResourceStatus.DEGRADED },
	{
		value: "unavailable",
		label: "Unavailable",
		status: ResourceStatus.UNAVAILABLE,
	},
	{ value: "deploying", label: "Deploying", status: ResourceStatus.DEPLOYING },
	{ value: NOT_DEPLOYED, label: "Not deployed" },
];

export interface EnvCell {
	status: ResourceStatus;
	tag: string;
	ready: number;
	desired: number;
	regions: string[];
	note: string;
	deployedMs: number;
	spec: ServiceDeploymentSpec | undefined;
}

export interface ResourceRow {
	resource: Resource;
	cells: Map<string, EnvCell>;
	cpuMilli: number;
	memMi: number;
	replicas: { min: number; max: number } | null;
	address: string | null;
	image: string;
	createdMs: number;
}

export interface DraftRow extends Omit<ResourceRow, "resource"> {
	draft: Draft;
	environmentId: string;
}

export type ListRow = ResourceRow | DraftRow;

export function isDraftRow(row: ListRow): row is DraftRow {
	return "draft" in row;
}

export function rowName(row: ListRow): string {
	return isDraftRow(row) ? row.draft.name : row.resource.name;
}

function newestFirst(a: Deployment, b: Deployment): number {
	return tsMs(b.createdAt) - tsMs(a.createdAt);
}

function envCell(resource: Resource, environment: Environment, deployments: Deployment[]): EnvCell | undefined {
	const status = environmentStatus(resource, environment.id);
	const first = status?.regions[0];
	if (status === undefined) return undefined;
	const active =
		deployments.find((d) => d.id === first?.deploymentId) ??
		deployments.find((d) => d.environmentId === environment.id);
	const image = active === undefined ? "" : deploymentImage(active);
	return {
		status: status.status,
		tag: image === "" ? "" : imageVersion(image),
		ready: status.regions.reduce((n, r) => n + r.readyReplicas, 0),
		desired: status.regions.reduce((n, r) => n + r.desiredReplicas, 0),
		regions: status.regions.map((r) => r.region),
		note: status.regions.find((r) => (r.message ?? "") !== "")?.message ?? "",
		deployedMs: tsMs(active?.createdAt),
		spec: active === undefined ? undefined : getServiceSpec(active),
	};
}

export function buildRows(
	resources: Resource[],
	deployments: Deployment[][],
	environments: Environment[],
	activeEnvironmentId: string | undefined,
): ResourceRow[] {
	return resources.map((resource, i) => {
		const mine = (deployments[i] ?? []).toSorted(newestFirst);
		const cells = new Map<string, EnvCell>();
		for (const env of environments) {
			const cell = envCell(resource, env, mine);
			if (cell !== undefined) cells.set(env.id, cell);
		}
		const spec = resource.spec?.spec;
		const region = configuredRegions(resource)[0];
		const target = spec?.case === "service" && region !== undefined ? spec.value.regions[region] : undefined;
		const running = activeEnvironmentId === undefined ? undefined : cells.get(activeEnvironmentId)?.spec;
		const minReplicas = running?.minReplicas ?? target?.minReplicas;
		const maxReplicas = running?.maxReplicas ?? target?.maxReplicas;
		const latest = mine[0];
		return {
			resource,
			cells,
			cpuMilli: parseCpuMilli(running?.cpu ?? target?.cpu ?? ""),
			memMi: parseMemMi(running?.memory ?? target?.memory ?? ""),
			replicas: minReplicas === undefined || maxReplicas === undefined ? null : { min: minReplicas, max: maxReplicas },
			address: primaryDomain(resource),
			image: latest === undefined ? "" : imageName(deploymentImage(latest)),
			createdMs: tsMs(resource.createdAt),
		};
	});
}

export function draftRows(
	drafts: readonly Draft[],
	environmentId: string,
	defaults: ServiceSizing | undefined,
): DraftRow[] {
	return drafts.map((draft) => {
		const size = defaults === undefined ? undefined : draftSizing(draft, defaults);
		return {
			draft,
			environmentId,
			cells: new Map(),
			cpuMilli: parseCpuMilli(size?.cpu ?? ""),
			memMi: parseMemMi(size?.memory ?? ""),
			replicas: size === undefined ? null : { min: size.replicas, max: size.replicas },
			address: null,
			image: imageName(draft.image),
			createdMs: draft.createdMs,
		};
	});
}

export function cellSummary(cell: EnvCell): string {
	if (cell.regions.length === 0) return "no region";
	const [first = "", ...rest] = cell.regions;
	const more = rest.length > 0 ? ` +${rest.length.toString()}` : "";
	return `${cell.ready.toString()}/${cell.desired.toString()} · ${first}${more}`;
}

export function replicasLabel(row: ListRow): string | null {
	if (row.replicas === null) return null;
	const { min, max } = row.replicas;
	return min === max ? min.toString() : `${min.toString()}–${max.toString()}`;
}

export const ENV_PREFIX = "env-";

const NO_CELL_RANK = statusRank(ResourceStatus.HEALTHY) + 1;

export function envRank(row: ListRow, environmentId: string): number {
	if (isDraftRow(row)) return row.environmentId === environmentId ? DRAFT_RANK : NO_CELL_RANK;
	const cell = row.cells.get(environmentId);
	return cell === undefined ? NO_CELL_RANK : statusRank(cell.status);
}

// The sorted row model falls back to input order on ties, in either direction,
// so feeding rows in name order makes every tie sort by name ascending.
export function nameOrder<T extends ListRow>(rows: T[]): T[] {
	return rows.toSorted((a, b) => rowName(a).localeCompare(rowName(b)));
}

export interface Filter {
	query: string;
	status: string;
	region: string;
}

function matchesQuery(row: ListRow, query: string): boolean {
	const q = query.trim().toLowerCase();
	if (q === "") return true;
	const cells = [...row.cells.values()];
	const fields = [
		rowName(row),
		isDraftRow(row) ? "" : (row.resource.description ?? ""),
		row.image,
		row.address ?? "",
		...cells.map((c) => c.tag),
		...cells.flatMap((c) => c.regions),
	];
	return fields.some((f) => f.toLowerCase().includes(q));
}

function matchesRegion(row: ListRow, region: string): boolean {
	return region === ALL || [...row.cells.values()].some((c) => c.regions.includes(region));
}

function matchesStatus(row: ListRow, status: string): boolean {
	if (status === ALL) return true;
	if (status === NOT_DEPLOYED) return row.cells.size === 0;
	const want = STATUS_FILTERS.find((f) => f.value === status)?.status;
	return [...row.cells.values()].some((c) => c.status === want);
}

export function filterRows<T extends ListRow>(rows: T[], filter: Filter): T[] {
	return rows.filter(
		(r) => matchesQuery(r, filter.query) && matchesRegion(r, filter.region) && matchesStatus(r, filter.status),
	);
}

export function statusCounts(rows: ListRow[], filter: Filter): Map<string, number> {
	const base = filterRows(rows, { ...filter, status: ALL });
	const counts = new Map<string, number>([[ALL, base.length]]);
	for (const f of STATUS_FILTERS) counts.set(f.value, base.filter((r) => matchesStatus(r, f.value)).length);
	return counts;
}

export function regionCounts(rows: ListRow[], filter: Filter): { region: string; count: number }[] {
	const base = filterRows(rows, { ...filter, status: ALL, region: ALL });
	const regions = new Set(rows.flatMap((r) => [...r.cells.values()].flatMap((c) => c.regions)));
	return [...regions].toSorted().map((region) => ({
		region,
		count: base.filter((r) => matchesRegion(r, region)).length,
	}));
}

export function linkEnvironment(row: ResourceRow, environments: Environment[], active: Environment | undefined) {
	if (active !== undefined && row.cells.has(active.id)) return active;
	return environments.find((e) => row.cells.has(e.id)) ?? active;
}

export interface TableColumn {
	id: string;
	enableSorting?: boolean;
	enableHiding?: boolean;
	meta?: { defaultHidden?: boolean };
}

const COLS_SEPARATOR = ",";

export const FILTER_URL_KEYS = { query: "q" };

export const FILTER_PARSERS = {
	query: parseAsString.withDefault(""),
	status: parseAsStringLiteral([ALL, ...STATUS_FILTERS.map((f) => f.value)]).withDefault(ALL),
	region: parseAsString.withDefault(ALL),
};

function defaultSort(columns: readonly TableColumn[]): ColumnSort {
	return {
		id: columns.find((c) => c.id.startsWith(ENV_PREFIX))?.id ?? "name",
		desc: false,
	};
}

function hideable(columns: readonly TableColumn[]): TableColumn[] {
	return columns.filter((c) => c.enableHiding !== false);
}

function visibleIds(columns: readonly TableColumn[], visibility: ColumnVisibilityState): string {
	return hideable(columns)
		.filter((c) => visibility[c.id] !== false)
		.map((c) => c.id)
		.join(COLS_SEPARATOR);
}

function defaultVisibility(columns: readonly TableColumn[]): ColumnVisibilityState {
	return Object.fromEntries(hideable(columns).map((c) => [c.id, c.meta?.defaultHidden !== true]));
}

export function sortableIds(columns: readonly TableColumn[]): string[] {
	return columns.filter((c) => c.enableSorting !== false).map((c) => c.id);
}

export function tableParsers(columns: readonly TableColumn[]) {
	return {
		...FILTER_PARSERS,
		sort: sortParser(sortableIds(columns), defaultSort(columns)),
		page: pageParser.withDefault(0),
		size: parseAsNumberLiteral(PAGE_SIZES).withDefault(PAGE_SIZES[0]),
		cols: createParser<ColumnVisibilityState>({
			parse: (raw) => {
				const wanted = new Set(raw.split(COLS_SEPARATOR));
				return Object.fromEntries(hideable(columns).map((c) => [c.id, wanted.has(c.id)]));
			},
			serialize: (visibility) => visibleIds(columns, visibility),
			eq: (a, b) => visibleIds(columns, a) === visibleIds(columns, b),
		}).withDefault(defaultVisibility(columns)),
	};
}
