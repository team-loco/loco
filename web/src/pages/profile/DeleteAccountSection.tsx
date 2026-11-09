import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";

import { deleteUser } from "@gen/loco/user/v1/user-UserService_connectquery";

import { useAuth } from "@/auth/AuthProvider";
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
import { DangerZone } from "@/components/design/DangerZone";
import { toastConnectError } from "@/lib/error-handler";

export function DeleteAccountSection({ userId }: { userId: string }) {
	const { logout } = useAuth();
	const navigate = useNavigate();
	const deleteUserMutation = useMutation(deleteUser);
	const [open, setOpen] = useState(false);

	const handleDeleteAccount = async () => {
		try {
			await deleteUserMutation.mutateAsync({ userId });
			toast.success("Account deleted successfully");
			void logout();
			void navigate("/login", { replace: true });
		} catch (error) {
			toastConnectError(error, "Failed to delete account");
		}
	};

	return (
		<>
			<DangerZone
				title="Delete account"
				description="Permanently removes your user and signs you out. This action cannot be undone."
				actionLabel="Delete account"
				blockedReason={null}
				onAction={() => {
					setOpen(true);
				}}
			/>
			<AlertDialog open={open} onOpenChange={setOpen}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete account?</AlertDialogTitle>
						<AlertDialogDescription>Are you sure? This action cannot be undone.</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel disabled={deleteUserMutation.isPending}>Cancel</AlertDialogCancel>
						<AlertDialogAction
							disabled={deleteUserMutation.isPending}
							onClick={() => {
								void handleDeleteAccount();
							}}
						>
							{deleteUserMutation.isPending ? "Deleting..." : "Delete account"}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</>
	);
}
