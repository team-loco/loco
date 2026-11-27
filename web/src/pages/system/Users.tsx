import { keepPreviousData } from "@tanstack/react-query";
import { listUsers } from "@gen/loco/user/v1/user-UserService_connectquery";
import type { User } from "@gen/loco/user/v1/user_pb";
import { UsersIcon } from "lucide-react";
import { parseAsNumberLiteral, parseAsString, useQueryStates } from "nuqs";

import { useTokenPage, useShownPage } from "@/hooks/useTokenPage";
import { SearchInput } from "@/components/design/SearchInput";
import { ErrorCard } from "@/components/ErrorCard";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { EmptyState } from "@/components/design/EmptyState";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { useMyScopes } from "@/hooks/useMyScopes";
import { pageTokensParser, tokenPageLabel, tokenPager } from "@/lib/paging";
import { systemAccess } from "@/lib/system";
import { userLabel } from "@/lib/user";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

import { UserDrawer } from "./UserDrawer";
import { SystemTime } from "./shared";

const PAGE_SIZES = [25, 50, 100] as const;
const PARAMS = {
	q: parseAsString.withDefault(""),
	pages: pageTokensParser,
	size: parseAsNumberLiteral(PAGE_SIZES).withDefault(PAGE_SIZES[0]),
	user: parseAsString,
};

const COLUMNS: DataColumn<User>[] = [
	{
		id: "name",
		header: "Name",
		size: 240,
		minSize: 140,
		meta: { primary: true },
		cell: ({ row: { original: u } }) => (
			<span className="flex min-w-0 items-center gap-2.5">
				<MemberAvatar name={u.name} email={u.email} avatarUrl={u.avatarUrl} className="size-6 shrink-0" />
				<CellText text={userLabel(u)} />
			</span>
		),
	},
	{
		id: "email",
		header: "Email",
		size: 280,
		minSize: 160,
		meta: { grow: true },
		cell: ({ row: { original: u } }) => <CellText text={u.email} className="font-mono text-[13px]" />,
	},
	{
		id: "joined",
		header: "Joined",
		size: 190,
		minSize: 150,
		meta: { hideOnMobile: true },
		cell: ({ row: { original: u } }) => <SystemTime ts={u.createdAt} />,
	},
];

export function SystemUsers() {
	const canChange = systemAccess(useMyScopes().scopes) === "admin";
	const [params, setParams] = useQueryStates(PARAMS, { history: "replace" });
	const loaded = useTokenPage(listUsers, { pageSize: params.size, query: params.q.trim() }, params.pages, {
		placeholderData: keepPreviousData,
	});
	const page = loaded.query;
	const users = page.data?.users ?? [];
	const nextPageToken = page.data?.nextPageToken ?? "";
	const pager = tokenPager(params.pages, nextPageToken, (pages) => {
		if (!page.isPlaceholderData) void setParams({ pages });
	});
	const shown = useShownPage(
		pager.page,
		tokenPageLabel(pager.page, params.size, users.length, loaded.total),
		page.isPlaceholderData,
	);
	const table = useDataTable("system-users", { columns: COLUMNS, data: users, getRowId: (u) => u.id });

	return (
		<Page>
			<PageHeader title="Users" />
			{page.error !== null ? (
				<ErrorCard error={page.error} fallbackMessage="Failed to load the users" minHeight="min-h-48" />
			) : (
				<Section
					search={
						<SearchInput
							value={params.q}
							maxLength={200}
							aria-label="Search users"
							placeholder="Search by name or email"
							onChange={(e) => {
								void setParams({ q: e.target.value || null, pages: null });
							}}
							onClear={() => {
								void setParams({ q: null, pages: null });
							}}
						/>
					}
				>
					<DataTable
						table={table}
						label="Users"
						band
						loading={page.isLoading}
						empty={<EmptyState icon={<UsersIcon />} title={params.q.trim() ? "No matches" : "No users"} />}
						activeRowId={params.user}
						onRowClick={(row) => {
							void setParams({ user: row.original.id });
						}}
						rowLabel={(row) => `Open ${userLabel(row.original)}`}
					/>
					<Pager
						label={shown.label}
						page={shown.page}
						loading={page.isPlaceholderData}
						total={users.length}
						pageSizes={PAGE_SIZES}
						pageSize={params.size}
						onPageSize={(size) => {
							void setParams({ size: PAGE_SIZES.find((n) => n === size) ?? null, pages: null });
						}}
						onFirst={pager.onFirst}
						onPrev={pager.onPrev}
						onNext={pager.onNext}
					/>
				</Section>
			)}
			<UserDrawer
				userId={params.user}
				canChange={canChange}
				onClose={() => {
					void setParams({ user: null });
				}}
			/>
		</Page>
	);
}
