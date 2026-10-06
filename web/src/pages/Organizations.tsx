import { useQuery } from "@connectrpc/connect-query";
import { Building2, Plus } from "lucide-react";
import { useState } from "react";

import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Organization } from "@gen/loco/org/v1/org_pb";

import { useAuth } from "@/auth/AuthProvider";
import { ErrorCard } from "@/components/ErrorCard";
import { Button } from "@/components/design/Button";
import { EmptyState } from "@/components/design/EmptyState";
import { Page, PageHeader, Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { useOrgWorkspace } from "@/context/ContextProvider";

import { CreateOrgDialog } from "./organizations/CreateOrgDialog";
import { DeleteOrgDialog } from "./organizations/DeleteOrgDialog";
import { OrgRow } from "./organizations/OrgRow";

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
	const { user } = useAuth();
	const { activeOrgId, setActiveOrg } = useOrgWorkspace();
	const [createOrgOpen, setCreateOrgOpen] = useState(false);
	const [deleteTarget, setDeleteTarget] = useState<Organization | null>(null);

	const {
		data: orgsRes,
		isLoading,
		error,
		refetch: refetchOrgs,
	} = useQuery(listUserOrgs, user ? { userId: user.id } : undefined, {
		enabled: !!user,
	});

	const orgs = orgsRes?.orgs ?? [];

	const openCreate = () => {
		setCreateOrgOpen(true);
	};

	const handleCreateOrgSuccess = (orgId: string) => {
		void refetchOrgs();
		setActiveOrg(orgId);
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
		return orgs.map((org) => (
			<OrgRow key={org.id} org={org} active={org.id === activeOrgId} onSwitch={setActiveOrg} onDelete={setDeleteTarget} />
		));
	};

	const countLabel = isLoading ? null : <span className="font-normal text-fg3">{orgs.length}</span>;
	const body = renderBody();

	return (
		<Page className="max-w-[1080px]">
			<PageHeader
				title="Organizations"
				actions={
					<Button onClick={openCreate}>
						<Plus />
						New organization
					</Button>
				}
			/>
			<Section title={<>Your organizations {countLabel}</>}>{body}</Section>

			<CreateOrgDialog open={createOrgOpen} onOpenChange={setCreateOrgOpen} onSuccess={handleCreateOrgSuccess} />
			<DeleteOrgDialog
				org={deleteTarget}
				onOpenChange={(open) => {
					if (!open) {
						setDeleteTarget(null);
					}
				}}
				onSuccess={() => {
					void refetchOrgs();
				}}
			/>
		</Page>
	);
}
