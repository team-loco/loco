import { useQuery } from "@connectrpc/connect-query";
import { listSystemRoles } from "@gen/loco/admin/v1/admin-SystemRoleService_connectquery";
import { SystemRole, type SystemRoleHolder, type UnclaimedEmail } from "@gen/loco/admin/v1/admin_pb";
import { SearchXIcon, ShieldCheckIcon, UserPlusIcon } from "lucide-react";
import { parseAsString, useQueryStates } from "nuqs";
import { useState } from "react";

import { ErrorCard } from "@/components/ErrorCard";
import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { EmptyState } from "@/components/design/EmptyState";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { SearchInput } from "@/components/design/SearchInput";
import { useMyScopes } from "@/hooks/useMyScopes";
import { effectiveRole, roleSource, SYSTEM_ROLE_LABEL, systemAccess } from "@/lib/system";

import { GrantRoleDialog, OperatorRowMenu } from "./OperatorActions";

const PARAMS = { q: parseAsString.withDefault("") };

function RoleBadge({ role }: { role: SystemRole }) {
	return <Badge tone={role === SystemRole.ADMIN ? "warn" : "info"}>{SYSTEM_ROLE_LABEL[role]}</Badge>;
}

function holderColumns(holders: readonly SystemRoleHolder[], canChange: boolean): DataColumn<SystemRoleHolder>[] {
	return [
		{
			id: "email",
			header: "Email",
			size: 280,
			minSize: 160,
			meta: { primary: true, fit: true, fitMax: 360 },
			cell: ({ row: { original: h } }) => <CellText text={h.email} className="font-mono text-[13px]" />,
		},
		{
			id: "role",
			header: "Role",
			size: 120,
			minSize: 90,
			cell: ({ row: { original: h } }) => <RoleBadge role={effectiveRole(h)} />,
		},
		{
			id: "source",
			header: "Comes from",
			size: 220,
			minSize: 140,
			meta: { grow: true },
			cell: ({ row: { original: h } }) => <CellText text={roleSource(h)} />,
		},
		...(canChange
			? [
					{
						id: "menu",
						meta: { pinEnd: true, label: "Actions" },
						cell: ({ row: { original: h } }: { row: { original: SystemRoleHolder } }) => (
							<OperatorRowMenu holder={h} holders={holders} />
						),
					} satisfies DataColumn<SystemRoleHolder>,
				]
			: []),
	];
}

const UNCLAIMED_COLUMNS: DataColumn<UnclaimedEmail>[] = [
	{
		id: "email",
		header: "Email",
		size: 280,
		minSize: 160,
		meta: { primary: true, fit: true, fitMax: 360 },
		cell: ({ row: { original: u } }) => <CellText text={u.email} className="font-mono text-[13px]" />,
	},
	{
		id: "role",
		header: "Role",
		size: 120,
		minSize: 90,
		cell: ({ row: { original: u } }) => <RoleBadge role={u.role} />,
	},
	{
		id: "note",
		header: "Note",
		size: 320,
		minSize: 160,
		meta: { grow: true },
		cell: () => <CellText text="Configured. Takes the role on first sign-in." />,
	},
];

export function SystemOperators() {
	const roles = useQuery(listSystemRoles, {});
	const canChange = systemAccess(useMyScopes().scopes) === "admin";
	const [{ q }, setParams] = useQueryStates(PARAMS, { history: "replace" });
	const [granting, setGranting] = useState(false);
	const needle = q.trim().toLowerCase();
	const all = roles.data?.holders ?? [];
	const holders = all.filter((h) => h.email.toLowerCase().includes(needle));
	const unclaimed = (roles.data?.unclaimed ?? []).filter((u) => u.email.toLowerCase().includes(needle));
	const holderTable = useDataTable("system-operators", {
		columns: holderColumns(all, canChange),
		data: holders,
		getRowId: (h) => h.userId,
	});
	const unclaimedTable = useDataTable("system-operators-unclaimed", {
		columns: UNCLAIMED_COLUMNS,
		data: unclaimed,
		getRowId: (u) => u.email,
	});
	const empty =
		needle !== "" ? (
			<EmptyState icon={<SearchXIcon />} title="No operators match" query={needle}>
				Clear the search to see everyone.
			</EmptyState>
		) : (
			<EmptyState icon={<ShieldCheckIcon />} title="No operators yet">
				Grant a role to a user, or list emails in the API configuration.
			</EmptyState>
		);

	return (
		<Page>
			<PageHeader
				title="Operators"
				actions={
					canChange && (
						<Button
							onClick={() => {
								setGranting(true);
							}}
						>
							<UserPlusIcon />
							Grant role
						</Button>
					)
				}
			/>
			{roles.error !== null ? (
				<ErrorCard error={roles.error} fallbackMessage="Failed to load the operators" minHeight="min-h-48" />
			) : (
				<>
					<Section
						title="Installation roles"
						search={
							<SearchInput
								value={q}
								onChange={(e) => {
									void setParams({ q: e.target.value === "" ? null : e.target.value });
								}}
								onClear={() => {
									void setParams({ q: null });
								}}
								placeholder="Filter by email"
								aria-label="Filter operators by email"
							/>
						}
					>
						<DataTable table={holderTable} label="Operators" band loading={roles.isLoading} empty={empty} />
					</Section>
					{unclaimed.length > 0 && (
						<Section title="Waiting to sign in" count={unclaimed.length}>
							<DataTable table={unclaimedTable} label="Waiting to sign in" band />
						</Section>
					)}
				</>
			)}
			<GrantRoleDialog
				open={granting}
				onClose={() => {
					setGranting(false);
				}}
			/>
		</Page>
	);
}
