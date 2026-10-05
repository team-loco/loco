import { useMutation } from "@connectrpc/connect-query";
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
	AlertDialogTrigger,
} from "@/components/design/AlertDialog";
import { Button } from "@/components/design/Button";
import { toastConnectError } from "@/lib/error-handler";

export function DeleteAccountSection({ userId }: { userId: string }) {
	const { logout } = useAuth();
	const navigate = useNavigate();
	const deleteUserMutation = useMutation(deleteUser);

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
		<section className="overflow-hidden rounded-lg border border-[color-mix(in_oklab,var(--red)_35%,var(--line))] bg-background">
			<div className="flex flex-wrap items-center gap-5 px-5 py-[18px]">
				<div className="flex min-w-0 flex-1 flex-col gap-1">
					<span className="font-semibold">Delete account</span>
					<span className="text-[12.5px] text-fg3">
						Permanently removes your user and signs you out. This action cannot be undone.
					</span>
				</div>
				<AlertDialog>
					<AlertDialogTrigger
						render={<Button variant="destructive-outline" size="lg" disabled={deleteUserMutation.isPending} />}
					>
						Delete account
					</AlertDialogTrigger>
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
			</div>
		</section>
	);
}
