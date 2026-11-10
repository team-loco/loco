import type { SortColumn } from "@/components/design/DataTable";
import type { SortingState } from "@tanstack/react-table";
import type { Deployment, ServiceDeploymentSpec } from "@gen/loco/deployment/v1/deployment_pb";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { ResourceStatus, type Resource } from "@gen/loco/resource/v1/resource_pb";

import { deploymentImage, getServiceSpec } from "@/lib/deployment-utils";
import { DRAFT_RANK, draftSizing, type Draft, type ServiceSizing } from "@/lib/drafts";
import { imageName, imageVersion } from "@/lib/image";
import { parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { configuredRegions } from "@/lib/resource-regions";
import { environmentStatus, primaryDomain, statusRank } from "@/lib/resource-status";
import { tsMs } from "@/lib/time";

export const ALL = "all";
export const NOT_DEPLOYED = "not-deployed";
export const PAGE_SIZES = [25, 50, 100] as const;

export const STATUS_FILTERS: { value: string; label: string; status?: ResourceStatus }[] = [
	{ value: "degraded", label: "Degraded", status: ResourceStatus.DEGRADED },
	{ value: "unavailable", label: "Unavailable", status: ResourceStatus.UNAVAILABLE },
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

function rowName(row: ListRow): string {
	return isDraftRow(row) ? row.draft.name : row.resource.name;
}

function newestFirst(a: Deployment, b: Deployment): number {
	return tsMs(b.createdAt) - tsMs(a.createdAt);
}

function envCell(resource: Resource, environment: Environment, deployments: Deployment[]): EnvCell | undefined {
	const status = environmentStatus(resource, environment.id);
	const first = status?.regions[0];
	if (status === undefined || first === undefined) return undefined;
	const active =
		deployments.find((d) => d.id === first.deploymentId) ?? deployments.find((d) => d.environmentId === environment.id);
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
	const [first = "", ...rest] = cell.regions;
	const more = rest.length > 0 ? ` +${rest.length.toString()}` : "";
	return `${cell.ready.toString()}/${cell.desired.toString()} · ${first}${more}`;
}

export function replicasLabel(row: ListRow): string | null {
	if (row.replicas === null) return null;
	const { min, max } = row.replicas;
	return min === max ? min.toString() : `${min.toString()}–${max.toString()}`;
}

export type ColumnKind = "name" | "env" | "size" | "replicas" | "cost" | "address" | "image" | "created";

export interface Column {
	key: string;
	kind: ColumnKind;
	label: string;
	width: number;
	sortable: boolean;
	shown: boolean;
	environment?: Environment;
}

const ENV_PREFIX = "env-";
const NAME_WIDTH = 180;
const ENV_WIDTH = 172;

const FIXED: Omit<Column, "key">[] = [
	{ kind: "size", label: "Size", width: 160, sortable: true, shown: true },
	{ kind: "replicas", label: "Replicas", width: 92, sortable: false, shown: false },
	{ kind: "cost", label: "Est. cost", width: 110, sortable: false, shown: true },
	{ kind: "address", label: "Address", width: 170, sortable: false, shown: false },
	{ kind: "image", label: "Image", width: 200, sortable: false, shown: false },
	{ kind: "created", label: "Created", width: 150, sortable: true, shown: false },
];

export function tableColumns(environments: Environment[]): Column[] {
	return [
		{ key: "name", kind: "name", label: "Resource", width: NAME_WIDTH, sortable: true, shown: true },
		...environments.map((environment) => ({
			key: `${ENV_PREFIX}${environment.name}`,
			kind: "env" as const,
			label: environment.name,
			width: ENV_WIDTH,
			sortable: true,
			shown: true,
			environment,
		})),
		...FIXED.map((c) => ({ ...c, key: c.kind })),
	];
}

export type SortDir = "asc" | "desc";

export interface Sort {
	key: string;
	dir: SortDir;
}

const NO_CELL_RANK = statusRank(ResourceStatus.HEALTHY) + 1;

function envRank(row: ListRow, environmentId: string): number {
	if (isDraftRow(row)) return row.environmentId === environmentId ? DRAFT_RANK : NO_CELL_RANK;
	const cell = row.cells.get(environmentId);
	return cell === undefined ? NO_CELL_RANK : statusRank(cell.status);
}

export function resourceColumns(columns: Column[]): SortColumn<ListRow>[] {
	return columns.map((column) => ({
		id: column.key,
		enableSorting: column.sortable,
		sortDescFirst: column.kind === "size" || column.kind === "created",
		sortFn:
			column.kind === "name"
				? (a, b) => rowName(a.original).localeCompare(rowName(b.original))
				: column.kind === "size"
					? (a, b) => a.original.cpuMilli - b.original.cpuMilli || a.original.memMi - b.original.memMi
					: "basic",
		accessorFn: (row) => {
			switch (column.kind) {
				case "name":
					return rowName(row);
				case "env":
					return envRank(row, column.environment?.id ?? "");
				case "size":
					return row.cpuMilli;
				case "created":
					return row.createdMs;
				case "replicas":
				case "cost":
				case "address":
				case "image":
					return "";
			}
		},
	}));
}

export function resourceSorting(sort: Sort): SortingState {
	return [{ id: sort.key, desc: sort.dir === "desc" }, ...(sort.key === "name" ? [] : [{ id: "name", desc: false }])];
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
	return [...regions]
		.toSorted()
		.map((region) => ({ region, count: base.filter((r) => matchesRegion(r, region)).length }));
}

export function linkEnvironment(row: ResourceRow, environments: Environment[], active: Environment | undefined) {
	if (active !== undefined && row.cells.has(active.id)) return active;
	return environments.find((e) => row.cells.has(e.id)) ?? active;
}

export interface TableState {
	filter: Filter;
	sort: Sort;
	page: number;
	size: number;
	visible: string[];
}

const PARAM = {
	query: "q",
	status: "status",
	region: "region",
	sort: "sort",
	page: "page",
	size: "size",
	cols: "cols",
};
const SORT_SEPARATOR = ".";
const COLS_SEPARATOR = ",";

function defaultSort(columns: Column[]): Sort {
	return { key: columns.find((c) => c.kind === "env")?.key ?? "name", dir: "asc" };
}

function defaultVisible(columns: Column[]): string[] {
	return columns.filter((c) => c.kind !== "name" && c.shown).map((c) => c.key);
}

function parseSort(raw: string | null, columns: Column[]): Sort {
	const at = raw?.lastIndexOf(SORT_SEPARATOR) ?? -1;
	if (raw === null || at === -1) return defaultSort(columns);
	const key = raw.slice(0, at);
	const dir = raw.slice(at + 1);
	if (!columns.some((c) => c.key === key && c.sortable) || (dir !== "asc" && dir !== "desc")) {
		return defaultSort(columns);
	}
	return { key, dir };
}

function positiveInt(raw: string | null): number | undefined {
	const n = Number(raw);
	return Number.isInteger(n) && n > 0 ? n : undefined;
}

export function readTableState(params: URLSearchParams, columns: Column[]): TableState {
	const status = params.get(PARAM.status) ?? ALL;
	const size = positiveInt(params.get(PARAM.size));
	const cols = params.get(PARAM.cols);
	const wanted = cols === null ? null : new Set(cols.split(COLS_SEPARATOR));
	return {
		filter: {
			query: params.get(PARAM.query) ?? "",
			status: STATUS_FILTERS.some((f) => f.value === status) ? status : ALL,
			region: params.get(PARAM.region) ?? ALL,
		},
		sort: parseSort(params.get(PARAM.sort), columns),
		page: (positiveInt(params.get(PARAM.page)) ?? 1) - 1,
		size: PAGE_SIZES.find((s) => s === size) ?? PAGE_SIZES[0],
		visible:
			wanted === null
				? defaultVisible(columns)
				: columns.filter((c) => c.kind !== "name" && wanted.has(c.key)).map((c) => c.key),
	};
}

function putParam(params: URLSearchParams, key: string, value: string, fallback: string) {
	if (value === fallback) params.delete(key);
	else params.set(key, value);
}

export function writeFilter(params: URLSearchParams, filter: Filter): URLSearchParams {
	const next = new URLSearchParams(params);
	putParam(next, PARAM.query, filter.query, "");
	putParam(next, PARAM.status, filter.status, ALL);
	putParam(next, PARAM.region, filter.region, ALL);
	return next;
}

export function writeTableState(params: URLSearchParams, state: TableState, columns: Column[]): URLSearchParams {
	const next = writeFilter(params, state.filter);
	const put = (key: string, value: string, fallback: string) => {
		putParam(next, key, value, fallback);
	};
	const sort = defaultSort(columns);
	put(PARAM.sort, `${state.sort.key}${SORT_SEPARATOR}${state.sort.dir}`, `${sort.key}${SORT_SEPARATOR}${sort.dir}`);
	put(PARAM.page, String(state.page + 1), "1");
	put(PARAM.size, String(state.size), String(PAGE_SIZES[0]));
	put(PARAM.cols, state.visible.join(COLS_SEPARATOR), defaultVisible(columns).join(COLS_SEPARATOR));
	return next;
}
