import { useState } from "react";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/auth/AuthProvider";
import { listUserOrgs } from "@/gen/loco/org/v1";
import { useQuery } from "@connectrpc/connect-query";
import Loader from "@/assets/loader.svg?react";
import { Plus } from "lucide-react";
import { OrgCard } from "@/components/org/OrgCard";
import { CreateOrgDialog } from "@/components/org/CreateOrgDialog";
import { DeleteOrgDialog } from "@/components/org/DeleteOrgDialog";
import { useOrgWorkspace } from "@/context/ContextProvider";
import type { Organization } from "@/gen/loco/org/v1/org_pb";

export function Organizations() {
	const { user } = useAuth();
	const [createOrgOpen, setCreateOrgOpen] = useState(false);
	const [deleteOrgId, setDeleteOrgId] = useState<string | null>(null);
	const [deleteOrgName, setDeleteOrgName] = useState("");

	const {
		data: orgsRes,
		isLoading: orgsLoading,
		refetch: refetchOrgs,
	} = useQuery(listUserOrgs, user ? { userId: user.id } : undefined, {
		enabled: !!user,
	});

	const orgs = orgsRes?.orgs ?? [];
	const { setActiveOrg } = useOrgWorkspace();

	const handleSwitchOrg = (orgId: string) => {
		setActiveOrg(orgId);
	};

	const handleDeleteOrg = (org: Organization) => {
		setDeleteOrgId(org.id);
		setDeleteOrgName(org.name);
	};

	const handleCreateOrgSuccess = (orgId: string) => {
		refetchOrgs();
		handleSwitchOrg(orgId);
	};

	const handleDeleteSuccess = () => {
		refetchOrgs();
	};

	if (orgsLoading) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<div className="text-center flex flex-col gap-2 items-center">
					<Loader className="w-8 h-8" />
					<p className="text-foreground font-base">Loading organizations...</p>
				</div>
			</div>
		);
	}

	return (
		<div className="w-full">
			<div className="flex items-center justify-end mb-8">
				<Button onClick={() => setCreateOrgOpen(true)}>
					<Plus className="size-4 mr-2" />
					Create Organization
				</Button>
			</div>

			{orgs.length === 0 ? (
				<div className="flex flex-col items-center justify-center min-h-96 border-2 border-dashed border-border rounded-lg">
					<div className="text-center max-w-md">
						<h3 className="text-xl font-semibold text-foreground mb-2">
							No organizations yet
						</h3>
						<p className="text-muted-foreground mb-6">
							Get started by creating your first organization to manage
							workspaces and deploy resources.
						</p>
						<Button onClick={() => setCreateOrgOpen(true)}>
							<Plus className="size-4 mr-2" />
							Create Your First Organization
						</Button>
					</div>
				</div>
			) : (
				<div className="grid grid-cols-1 md:grid-cols-2 gap-4">
					{orgs.map((org) => (
						<OrgCard
							key={org.id.toString()}
							org={org}
							onSwitch={handleSwitchOrg}
							onDelete={handleDeleteOrg}
						/>
					))}
				</div>
			)}

			{/* Dialogs */}
			<CreateOrgDialog
				open={createOrgOpen}
				onOpenChange={setCreateOrgOpen}
				onSuccess={handleCreateOrgSuccess}
			/>
			{deleteOrgId && (
				<DeleteOrgDialog
					open={deleteOrgId !== null}
					onOpenChange={(open) => {
						if (!open) {
							setDeleteOrgId(null);
							setDeleteOrgName("");
						}
					}}
					orgId={deleteOrgId}
					orgName={deleteOrgName}
					onSuccess={handleDeleteSuccess}
				/>
			)}
		</div>
	);
}
