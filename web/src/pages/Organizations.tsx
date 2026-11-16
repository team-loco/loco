import { useQuery, useTransport } from "@connectrpc/connect-query";
import { useQuery as useTanstackQuery } from "@tanstack/react-query";
import { Building2, Plus } from "lucide-react";
import { parseAsInteger, useQueryState } from "nuqs";
import { useNavigate } from "react-router";

import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";

import { useAuth } from "@/auth/AuthProvider";
import { ErrorCard } from "@/components/ErrorCard";
import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { LimitCount, LimitedAction } from "@/components/design/LimitedAction";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Pager } from "@/components/design/Pager";
import { SoonTag } from "@/components/design/SoonTag";
import { Table, TableBody, TableHead, TableHeader, TableRow } from "@/components/design/Table";
import { useMyScopes } from "@/hooks/useMyScopes";
import { userOrgsOptions } from "@/hooks/userOrgs";
import { usePagedRows } from "@/lib/paging";
import { workspacePath } from "@/lib/routes";
import { Skeleton } from "@/components/design/Skeleton";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { limitReason, limitUsage } from "@/lib/limits";

import { CreateOrgDialog } from "@/components/shell/CreateOrgDialog";
import { OrgRow } from "./organizations/OrgRow";

const PAGE_SIZES = [25, 50, 100] as const;

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
	const transport = useTransport();
	const scopes = useMyScopes();
	const { data: orgsRes, isLoading, error } = useTanstackQuery(userOrgsOptions(transport, user?.id ?? ""));
	const orgs = orgsRes?.orgs ?? [];
	const paged = usePagedRows(orgs, PAGE_SIZES);
	const config = useQuery(getConfig, {});
	const created = orgs.filter((org) => org.createdBy === user?.id).length;
	const usage =
		isLoading || error
			? null
			: limitUsage(created, config.data?.countLimits?.orgsPerUser, "organizations you created", "");
	const blocked = limitReason(usage);

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
		if (orgs.length === 0) {
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
				<Table aria-label="Organizations">
					<TableHeader>
						<TableRow>
							<TableHead>Organization</TableHead>
							<TableHead>Your role</TableHead>
							<TableHead className="hidden text-right min-[701px]:table-cell">
								Workspaces <SoonTag />
							</TableHead>
							<TableHead className="hidden text-right min-[701px]:table-cell">
								Members <SoonTag />
							</TableHead>
							<TableHead className="hidden min-[701px]:table-cell">Created</TableHead>
							<TableHead>
								<span className="sr-only">Actions</span>
							</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{paged.rows.map((org) => (
							<OrgRow
								key={org.id}
								org={org}
								active={org.id === activeOrgId}
								onSwitch={setActiveOrg}
								role={
									org.createdBy === user?.id
										? "Owner"
										: scopes.isLoading
											? undefined
											: scopes.orgLevel(org.id) >= 3
												? "Admin"
												: "Member"
								}
							/>
						))}
					</TableBody>
				</Table>
				<Pager {...paged.pager} {...paged.ends} />
			</>
		);
	};

	const body = renderBody();

	return (
		<Page className="max-w-[1080px]">
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
				existingNames={orgs.map((org) => org.name)}
			/>
		</Page>
	);
}
