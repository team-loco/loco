import { useQuery } from "@connectrpc/connect-query";
import { listUsers } from "@gen/loco/user/v1/user-UserService_connectquery";
import type { User } from "@gen/loco/user/v1/user_pb";
import { UsersIcon } from "lucide-react";
import { parseAsNumberLiteral, parseAsString, useQueryStates } from "nuqs";

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
		meta: { primary: true, fit: true, fitMax: 320 },
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
	const page = useQuery(listUsers, { pageSize: params.size, pageToken: params.pages.at(-1) ?? "" });
	const users = page.data?.users ?? [];
	const nextPageToken = page.data?.nextPageToken ?? "";
	const pager = tokenPager(params.pages, nextPageToken, (pages) => {
		void setParams({ pages });
	});
	const table = useDataTable("system-users", { columns: COLUMNS, data: users, getRowId: (u) => u.id });

	return (
		<Page>
			<PageHeader title="Users" />
			{page.error !== null ? (
				<ErrorCard error={page.error} fallbackMessage="Failed to load the users" minHeight="min-h-48" />
			) : (
				<Section>
					<DataTable
						table={table}
						label="Users"
						band
						loading={page.isLoading}
						empty={<EmptyState icon={<UsersIcon />} title="No users" />}
						activeRowId={params.user}
						onRowClick={(row) => {
							void setParams({ user: row.original.id });
						}}
						rowLabel={(row) => `Open ${userLabel(row.original)}`}
					/>
					<Pager
						label={tokenPageLabel(pager.page, params.size, users.length, undefined)}
						page={pager.page}
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
