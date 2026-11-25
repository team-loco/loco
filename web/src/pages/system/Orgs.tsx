import { listOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { OrgOrder, OrgState, type OrgSummary } from "@gen/loco/org/v1/org_pb";
import { keepPreviousData } from "@tanstack/react-query";
import { functionalUpdate } from "@tanstack/react-table";
import { Building2Icon, CircleDotIcon, SearchXIcon } from "lucide-react";
import { parseAsBoolean, parseAsNumberLiteral, parseAsString, parseAsStringLiteral, useQueryStates } from "nuqs";
import { useNavigate } from "react-router";

import { ErrorCard } from "@/components/ErrorCard";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { EmptyState } from "@/components/design/EmptyState";
import { FilterMenu } from "@/components/design/FilterMenu";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SearchInput } from "@/components/design/SearchInput";
import { useShownPage, useTokenPage } from "@/hooks/useTokenPage";
import { formatCount } from "@/lib/format";
import { pageTokensParser, tokenPageLabel, tokenPager } from "@/lib/paging";
import { orgPath } from "@/lib/system";
import { userLabel } from "@/lib/user";

import { OrgStateBadge, SystemDate } from "./shared";

const PAGE_SIZES = [25, 50, 100] as const;
const STATES = ["active", "suspended"] as const;
const SORTS = ["name", "created"] as const;
const ALL = "all";
const MAX_QUERY = 200;
const COUNT_CELL = "font-mono text-[12.5px]";

const PARAMS = {
	q: parseAsString.withDefault(""),
	state: parseAsStringLiteral(STATES),
	sort: parseAsStringLiteral(SORTS).withDefault("created"),
	desc: parseAsBoolean.withDefault(true),
	pages: pageTokensParser,
	size: parseAsNumberLiteral(PAGE_SIZES).withDefault(PAGE_SIZES[0]),
};

const STATE_OF = { active: OrgState.ACTIVE, suspended: OrgState.SUSPENDED } as const;
const ORDER_OF = { name: OrgOrder.NAME, created: OrgOrder.CREATED_AT } as const;
const STATE_OPTIONS = [
	{ value: ALL, label: "All" },
	{ value: "active", label: "Active" },
	{ value: "suspended", label: "Suspended" },
];

function Owners({ summary }: { summary: OrgSummary }) {
	const [first, ...rest] = summary.owners;
	if (first === undefined) return <span className="text-fg3">None</span>;
	return (
		<span className="flex min-w-0 items-center gap-1.5">
			<CellText text={userLabel(first)} />
			{rest.length > 0 && <span className="text-fg3">+{rest.length}</span>}
		</span>
	);
}

function count(value: bigint | number | undefined): string {
	return value === undefined ? "—" : formatCount(Number(value));
}

const COLUMNS: DataColumn<OrgSummary>[] = [
	{
		id: "name",
		header: "Organization",
		size: 160,
		minSize: 110,
		accessorFn: (r) => r.organization?.name,
		meta: { grow: true, primary: true },
		cell: ({ row: { original: r } }) => <CellText text={r.organization?.name ?? ""} className="text-foreground" />,
	},
	{
		id: "owner",
		header: "Owner",
		size: 150,
		minSize: 110,
		meta: { hideOnMobile: true },
		cell: ({ row: { original: r } }) => <Owners summary={r} />,
	},
	{
		id: "state",
		header: "State",
		size: 115,
		minSize: 100,
		cell: ({ row: { original: r } }) => (r.organization === undefined ? null : <OrgStateBadge org={r.organization} />),
	},
	{
		id: "members",
		header: "Members",
		size: 90,
		minSize: 80,
		meta: { hideOnMobile: true, align: "end", cellClassName: COUNT_CELL },
		cell: ({ row: { original: r } }) => count(r.organization?.memberCount),
	},
	{
		id: "workspaces",
		header: "Workspaces",
		size: 105,
		minSize: 90,
		meta: { hideOnMobile: true, align: "end", cellClassName: COUNT_CELL },
		cell: ({ row: { original: r } }) => count(r.organization?.workspaceCount),
	},
	{
		id: "resources",
		header: "Resources",
		size: 95,
		minSize: 80,
		meta: { hideOnMobile: true, align: "end", cellClassName: COUNT_CELL },
		cell: ({ row: { original: r } }) => count(r.resourceCount),
	},
	{
		id: "created",
		header: "Created",
		size: 115,
		minSize: 100,
		accessorFn: (r) => r.organization?.createdAt?.seconds,
		meta: { hideOnMobile: true, cellClassName: "whitespace-nowrap text-[12.5px] text-fg3" },
		cell: ({ row: { original: r } }) => <SystemDate ts={r.organization?.createdAt} />,
	},
];

export function SystemOrgs() {
	const navigate = useNavigate();
	const [params, setParams] = useQueryStates(PARAMS, { history: "replace" });
	const { q, state, sort, desc, size } = params;
	const needle = q.trim();
	const input = {
		query: needle,
		state: state === null ? OrgState.UNSPECIFIED : STATE_OF[state],
		orderBy: ORDER_OF[sort],
		descending: desc,
	};
	const page = useTokenPage(
		listOrgs,
		{ ...input, pageSize: size },
		params.pages,
		{ placeholderData: keepPreviousData },
		input,
	);
	const rows = page.query.data?.orgs ?? [];
	const pager = tokenPager(params.pages, page.nextPageToken, (pages) => {
		if (!page.query.isPlaceholderData) void setParams({ pages });
	});
	const shown = useShownPage(
		pager.page,
		tokenPageLabel(pager.page, size, rows.length, page.total),
		page.query.isPlaceholderData,
	);
	const sorting = [{ id: sort, desc }];
	const table = useDataTable("system-orgs", {
		columns: COLUMNS,
		data: rows,
		getRowId: (r) => r.organization?.id ?? "",
		state: { sorting },
		manualSorting: true,
		onSortingChange: (updater) => {
			const next = functionalUpdate(updater, sorting)[0];
			if (next?.id === "name" || next?.id === "created") {
				void setParams({ sort: next.id, desc: next.desc, pages: null });
			}
		},
	});
	const filtered = needle !== "" || state !== null;
	const empty = filtered ? (
		<EmptyState icon={<SearchXIcon />} title="No organizations match" query={needle}>
			Clear the search or the state filter to see the others.
		</EmptyState>
	) : (
		<EmptyState icon={<Building2Icon />} title="No organizations yet">
			Organizations appear here once people create them.
		</EmptyState>
	);

	return (
		<Page>
			<PageHeader title="Organizations" />
			<Section
				filters={
					<FilterMenu
						label="State"
						icon={<CircleDotIcon />}
						value={state ?? ALL}
						options={STATE_OPTIONS}
						onChange={(value) => {
							void setParams({ state: STATES.find((s) => s === value) ?? null, pages: null });
						}}
					/>
				}
				search={
					<SearchInput
						value={q}
						maxLength={MAX_QUERY}
						onChange={(e) => {
							void setParams({ q: e.target.value === "" ? null : e.target.value, pages: null });
						}}
						onClear={() => {
							void setParams({ q: null, pages: null });
						}}
						placeholder="Search by name, owner email or ID"
						aria-label="Search organizations"
					/>
				}
			>
				{page.error !== null ? (
					<ErrorCard error={page.error} fallbackMessage="Failed to load the organizations" minHeight="min-h-48" />
				) : (
					<>
						<DataTable
							table={table}
							label="Organizations"
							band
							loading={page.query.isPending}
							empty={empty}
							rowAction="navigate"
							rowLabel={(row) => `Open ${row.original.organization?.name ?? ""}`}
							onRowClick={(row) => {
								void navigate(orgPath(row.original.organization?.id ?? ""));
							}}
						/>
						<Pager
							label={shown.label}
							page={shown.page}
							total={rows.length}
							pageSizes={PAGE_SIZES}
							pageSize={size}
							onPageSize={(next) => {
								void setParams({ size: PAGE_SIZES.find((n) => n === next) ?? null, pages: null });
							}}
							loading={page.query.isPlaceholderData}
							onFirst={pager.onFirst}
							onPrev={pager.onPrev}
							onNext={pager.onNext}
						/>
					</>
				)}
			</Section>
		</Page>
	);
}
