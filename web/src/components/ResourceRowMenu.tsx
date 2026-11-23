import { useMutation, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { Trash2Icon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { toast } from "@/lib/toast";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";
import { Scope } from "@gen/loco/token/v1/token_pb";
import { deleteResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { DeleteResourceDialog } from "@/components/DeleteResourceDialog";
import { DropdownMenuItem } from "@/components/design/DropdownMenu";
import { RowMenu } from "@/components/design/RowMenu";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { NEEDS_ADMIN, scopeLevel, useMyScopes } from "@/hooks/useMyScopes";
import { workspaceResourcesKey } from "@/hooks/workspaceResources";
import { toastConnectError } from "@/lib/error-handler";

export function ResourceRowMenu({
	resource,
	workspaceId,
	title,
	monoTitle = false,
	children,
}: {
	resource: Resource;
	workspaceId: string;
	title?: ReactNode;
	monoTitle?: boolean;
	children: ReactNode;
}) {
	const queryClient = useQueryClient();
	const transport = useTransport();
	const [confirmOpen, setConfirmOpen] = useState(false);
	const deleteMutation = useMutation(deleteResource);
	const { activeOrgId } = useOrgWorkspace();
	const { resourceLevel } = useMyScopes();
	const canDelete = resourceLevel(activeOrgId ?? "", workspaceId, resource.id) >= scopeLevel(Scope.ADMIN);

	const confirmDelete = async () => {
		try {
			await deleteMutation.mutateAsync({ resourceId: resource.id });
			await queryClient.invalidateQueries({ queryKey: workspaceResourcesKey(transport, workspaceId) });
			toast.success(`Deleted ${resource.name}`);
			setConfirmOpen(false);
		} catch (err) {
			toastConnectError(err, "Failed to delete resource");
		}
	};

	return (
		<>
			<RowMenu label={`Actions for ${resource.name}`} title={title} monoTitle={monoTitle}>
				{children}
				<DropdownMenuItem
					variant="destructive"
					disabled={!canDelete}
					title={canDelete ? undefined : NEEDS_ADMIN}
					onClick={() => {
						setConfirmOpen(true);
					}}
				>
					<Trash2Icon />
					Delete
				</DropdownMenuItem>
			</RowMenu>
			<DeleteResourceDialog
				open={confirmOpen}
				onOpenChange={setConfirmOpen}
				resource={resource}
				confirmLabel="Delete"
				pending={deleteMutation.isPending}
				onConfirm={() => {
					void confirmDelete();
				}}
			/>
		</>
	);
}
