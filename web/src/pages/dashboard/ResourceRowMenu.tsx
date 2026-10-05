import { createConnectQueryKey, useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EllipsisIcon } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";
import {
	deleteResource,
	listWorkspaceResources,
} from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/design/AlertDialog";
import { Button } from "@/components/design/Button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { toastConnectError } from "@/lib/error-handler";
import { observabilityPath, resourcePath } from "@/lib/routes";

export function ResourceRowMenu({
	resource,
	orgId,
	workspaceId,
	envName,
}: {
	resource: Resource;
	orgId: string;
	workspaceId: string;
	envName: string;
}) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const [confirmOpen, setConfirmOpen] = useState(false);
	const deleteMutation = useMutation(deleteResource);

	const base = resourcePath(orgId, workspaceId, resource.id);
	const envQuery = `env=${encodeURIComponent(envName)}`;
	const obs = (view: "logs" | "events") =>
		observabilityPath(orgId, workspaceId, view, { env: envName, r: resource.name });

	const confirmDelete = async () => {
		try {
			await deleteMutation.mutateAsync({ resourceId: resource.id });
			const key = createConnectQueryKey({
				schema: listWorkspaceResources,
				input: { workspaceId, pageSize: 200 },
				cardinality: "finite",
			});
			await queryClient.invalidateQueries({ queryKey: key });
			toast.success(`${resource.name} deleted`);
			setConfirmOpen(false);
		} catch (err) {
			toastConnectError(err, "Failed to delete resource");
		}
	};

	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger
					render={
						<Button
							variant="ghost"
							size="icon-sm"
							aria-label={`Actions for ${resource.name}`}
							className="text-fg2 hover:border-line hover:bg-background dark:hover:bg-background"
						/>
					}
				>
					<EllipsisIcon className="size-4" />
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end" className="w-auto min-w-[180px]">
					<DropdownMenuItem onClick={() => void navigate(obs("logs"))}>View logs</DropdownMenuItem>
					<DropdownMenuItem onClick={() => void navigate(obs("events"))}>View events</DropdownMenuItem>
					<DropdownMenuItem onClick={() => void navigate(`${base}?tab=settings&${envQuery}`)}>Scale</DropdownMenuItem>
					<DropdownMenuItem onClick={() => void navigate(`${base}?tab=variables&${envQuery}`)}>
						Environment variables
					</DropdownMenuItem>
					<DropdownMenuSeparator />
					<DropdownMenuItem
						variant="destructive"
						onClick={() => {
							setConfirmOpen(true);
						}}
					>
						Delete
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			<AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete {resource.name}?</AlertDialogTitle>
						<AlertDialogDescription>
							This removes the resource, its deployments in every environment, and its domains. It cannot be
							undone.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction
							disabled={deleteMutation.isPending}
							onClick={() => {
								void confirmDelete();
							}}
						>
							{deleteMutation.isPending ? "Deleting…" : "Delete resource"}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</>
	);
}
