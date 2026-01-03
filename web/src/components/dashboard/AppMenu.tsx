import { MoreVertical, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useNavigate } from "react-router";
import type { Resource } from "@/gen/resource/v1/resource_pb";
import { useMutation } from "@connectrpc/connect-query";
import { deleteResource } from "@/gen/resource/v1";
import { useState } from "react";

interface AppMenuProps {
	resource: Resource;
	onResourceDeleted?: () => void;
}

export function AppMenu({ resource, onResourceDeleted }: AppMenuProps) {
	const navigate = useNavigate();
	const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

	const deleteResourceMutation = useMutation(deleteResource);

	const handleDelete = async () => {
		try {
			await deleteResourceMutation.mutateAsync({ resourceId: resource.id });
			setShowDeleteConfirm(false);
			onResourceDeleted?.();
		} catch (error) {
			console.error("Failed to delete resource:", error);
		}
	};

	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button
						variant="ghost"
						size="sm"
						className="h-8 w-8 p-0"
						onClick={(e) => e.stopPropagation()}
					>
						<MoreVertical className="h-4 w-4" />
					</Button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end">
					<DropdownMenuItem
						onClick={(e) => {
							e.stopPropagation();
							navigate(`/resource/${resource.id}/settings`);
						}}
					>
						Settings
					</DropdownMenuItem>
					<DropdownMenuItem
						onClick={(e) => {
							e.stopPropagation();
							setShowDeleteConfirm(true);
						}}
						className="text-destructive"
					>
						<Trash2 className="h-4 w-4 mr-2" />
						Delete
					</DropdownMenuItem>
					</DropdownMenuContent>
					</DropdownMenu>

					{/* Delete Confirmation Dialog */}
					{showDeleteConfirm && (
					<div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center">
					<div className="bg-background border-2 border-border rounded-lg p-6 max-w-sm">
						<h3 className="text-lg font-heading mb-2">Delete Resource</h3>
						<p className="text-sm text-foreground opacity-70 mb-4">
							Are you sure you want to delete <strong>{resource.name}</strong>? This
							action cannot be undone.
						</p>
						<div className="flex gap-2 justify-end">
							<Button
								variant="outline"
								onClick={() => setShowDeleteConfirm(false)}
								className="border-2"
							>
								Cancel
							</Button>
							<Button
								variant="destructive"
								onClick={handleDelete}
								disabled={deleteResourceMutation.isPending}
							>
								{deleteResourceMutation.isPending ? "Deleting..." : "Delete"}
							</Button>
						</div>
					</div>
				</div>
			)}
		</>
	);
}
