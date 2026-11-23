import { useQuery } from "@connectrpc/connect-query";
import { Building2, Plus } from "lucide-react";
import { parseAsInteger, parseAsNumberLiteral, useQueryState, useQueryStates } from "nuqs";
import { useNavigate } from "react-router";

import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";

import { useAuth } from "@/auth/AuthProvider";
import { ErrorCard } from "@/components/ErrorCard";
import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { LimitCount, LimitedAction } from "@/components/design/LimitedAction";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { DataTable, useDataTable } from "@/components/design/DataTable";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useTokenPage } from "@/hooks/useTokenPage";
import { pageTokensParser, tokenPageLabel, tokenPager } from "@/lib/paging";
import { workspacePath } from "@/lib/routes";
import { Skeleton } from "@/components/design/Skeleton";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { limitReason, limitUsage } from "@/lib/limits";

import { CreateOrgDialog } from "@/components/shell/CreateOrgDialog";
import { orgColumns } from "./organizations/columns";

const PAGE_SIZES = [25, 50, 100] as const;
const PAGE_PARAMS = { pages: pageTokensParser, size: parseAsNumberLiteral(PAGE_SIZES).withDefault(PAGE_SIZES[0]) };

function OrgListSkeleton() {
	return (
		<div>
			{[0, 1, 2].map((i) => (
				<div key={i} className="flex items-center gap-3 border-b border-line px-5 py-3.5 last:border-b-0">
					<Skeleton className="size-4" />
					<Skeleton className="h-3.5 w-40" />
					<div className="flex-1" />
					<Skeleton className="h-7 w-44" />
				</div>
			))}
		</div>
	);
}

export function Organizations() {
	const navigate = useNavigate();
	const { user } = useAuth();
	const { activeOrgId, setActiveOrg } = useOrgWorkspace();
	const [createOrgOpen, setCreateOrgOpen] = useQueryState("new", parseAsInteger.withDefault(0));
	const scopes = useMyScopes();
	const [params, setParams] = useQueryStates(PAGE_PARAMS, { history: "replace" });
	const { query, nextPageToken, total } = useTokenPage(
		listUserOrgs,
		{ userId: user?.id ?? "", pageSize: params.size },
		params.pages,
		{ enabled: user !== null },
	);
	const { isLoading, error } = query;
	const orgs = query.data?.orgs ?? [];
	const pager = tokenPager(params.pages, nextPageToken, (pages) => {
		void setParams({ pages });
	});
	const config = useQuery(getConfig, {});
	const usage =
		isLoading || error || query.data === undefined
			? null
			: limitUsage(
					query.data.createdCount,
					config.data?.countLimits?.orgsPerUser,
					"organizations you created",
					"account",
				);
	const blocked = limitReason(usage);
	const table = useDataTable("organizations", {
		columns: orgColumns({
			activeOrgId,
			roleOf: (orgId) => (scopes.isLoading ? undefined : scopes.orgLevel(orgId) >= 3 ? "Admin" : "Member"),
			onSwitch: setActiveOrg,
		}),
		data: orgs,
		getRowId: (org) => org.id,
	});

	const openCreate = () => {
		void setCreateOrgOpen(1);
	};

	const handleCreateOrgSuccess = (orgId: string, workspaceId: string) => {
		void navigate(workspacePath(orgId, workspaceId));
	};

	const renderBody = () => {
		if (isLoading) {
			return <OrgListSkeleton />;
		}
		if (error) {
			return <ErrorCard error={error} fallbackMessage="Failed to load organizations" minHeight="min-h-48" />;
		}
		if (orgs.length === 0 && params.pages.length === 0) {
			return (
				<EmptyState
					icon={<Building2 />}
					title="No organizations yet"
					action={
						<Button onClick={openCreate}>
							<Plus />
							Create your first organization
						</Button>
					}
				>
					Get started by creating your first organization to manage workspaces and deploy resources.
				</EmptyState>
			);
		}
		return (
			<>
				<DataTable
					table={table}
					label="Organizations"
					band
					onRowClick={(row) => {
						setActiveOrg(row.id);
					}}
					rowAction="navigate"
					rowLabel={(row) => `Open ${row.original.name}`}
				/>
				<Pager
					label={tokenPageLabel(pager.page, params.size, orgs.length, total)}
					page={pager.page}
					total={orgs.length}
					pageSizes={PAGE_SIZES}
					pageSize={params.size}
					onPageSize={(size) => {
						void setParams({ size: PAGE_SIZES.find((n) => n === size) ?? null, pages: null });
					}}
					onFirst={pager.onFirst}
					onPrev={pager.onPrev}
					onNext={pager.onNext}
				/>
			</>
		);
	};

	const body = renderBody();

	return (
		<Page>
			<PageHeader
				title={
					<>
						Organizations
						<LimitCount usage={usage} />
					</>
				}
				actions={
					<LimitedAction reason={blocked}>
						<Button disabled={blocked !== null} onClick={openCreate}>
							<Plus />
							New organization
						</Button>
					</LimitedAction>
				}
			/>
			<Section>{body}</Section>

			<CreateOrgDialog
				open={createOrgOpen === 1}
				onOpenChange={(open) => {
					void setCreateOrgOpen(open ? 1 : null);
				}}
				onSuccess={handleCreateOrgSuccess}
			/>
		</Page>
	);
}
