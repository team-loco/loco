import { useAuth } from "@/auth/AuthProvider";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { deleteUser, whoAmI } from "@/gen/user/v1";
import { toastConnectError } from "@/lib/error-handler";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";
import Loader from "@/assets/loader.svg?react";

export function Profile() {
	const { logout } = useAuth();
	const navigate = useNavigate();
	const { data: whoAmIResponse, isLoading } = useQuery(whoAmI, {});
	const user = whoAmIResponse?.user;

	const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
	const deleteUserMutation = useMutation(deleteUser);

	if (isLoading) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<div className="text-center flex flex-col gap-2 items-center">
					<Loader className="w-8 h-8" />
					<p className="text-foreground font-base">Loading...</p>
				</div>
			</div>
		);
	}

	if (!user) {
		return <div>User not found</div>;
	}

	const handleDeleteAccount = async () => {
		try {
			await deleteUserMutation.mutateAsync({ userId: user.id });
			toast.success("Account deleted successfully");
			logout();
			navigate("/login", { replace: true });
		} catch (error) {
			toastConnectError(error);
			console.error(error);
		}
	};

	return (
		<div className="mx-auto py-8">
			<div className="mb-8">
				<h1 className="text-3xl text-foreground mb-2">Profile Settings</h1>
				<p className="text-muted-foreground">
					Manage your account and preferences
				</p>
			</div>

			{/* Profile Info */}
			<Card className="mb-6">
				<CardHeader>
					<CardTitle className="text-lg">Account Information</CardTitle>
				</CardHeader>
				<CardContent>
					<div className="flex items-center gap-4">
						<Avatar className="h-12 w-12">
							<AvatarImage src={user.avatarUrl} alt="user avatar" />
							<AvatarFallback>
								{user.name.charAt(0).toUpperCase()}
							</AvatarFallback>
						</Avatar>
						<div className="flex-1">
							<div className="flex gap-8">
								<div>
									<p className="text-sm text-muted-foreground">Name</p>
									<p className="text-foreground font-medium">{user.name}</p>
								</div>
								<div>
									<p className="text-sm text-muted-foreground">Email</p>
									<p className="text-foreground font-medium">{user.email}</p>
								</div>
							</div>
						</div>
					</div>
				</CardContent>
			</Card>

			{/* API Tokens */}
			<Card className="mb-6">
				<CardHeader>
					<CardTitle className="text-lg">API Tokens</CardTitle>
					<CardDescription>
						Manage API tokens for programmatic access
					</CardDescription>
				</CardHeader>
				<CardContent>
					<div className="space-y-4">
						<p className="text-sm text-muted-foreground italic">
							Coming Soon once Aji finishes TVM!!
						</p>
						<Button variant="secondary" disabled>
							Create New Token
						</Button>
					</div>
				</CardContent>
			</Card>

			{/* Logout & Account Management */}
			<Card className="mb-6">
				<CardHeader>
					<CardTitle className="text-lg">Account</CardTitle>
				</CardHeader>
				<CardContent className="space-y-3">
					<Button variant="secondary" className="w-full" onClick={logout}>
						Logout
					</Button>

					{!showDeleteConfirm ? (
						<Button
							variant="secondary"
							className="w-full text-error-text border-error-border bg-error-bg hover:bg-error-bg/80"
							onClick={() => setShowDeleteConfirm(true)}
							disabled={deleteUserMutation.isPending}
						>
							Delete Account
						</Button>
					) : (
						<div className="space-y-2 p-4 border-2 border-error-border rounded-lg bg-error-bg">
							<p className="text-sm text-error-text font-medium">
								Are you sure? This action cannot be undone.
							</p>
							<div className="flex gap-2">
								<Button
									variant="secondary"
									className="flex-1 text-sm"
									onClick={() => setShowDeleteConfirm(false)}
									disabled={deleteUserMutation.isPending}
								>
									Cancel
								</Button>
								<Button
									className="flex-1 text-sm bg-error-bg text-error-text border-error-border hover:bg-error-bg/80"
									onClick={handleDeleteAccount}
									disabled={deleteUserMutation.isPending}
								>
									{deleteUserMutation.isPending ? "Deleting..." : "Delete"}
								</Button>
							</div>
						</div>
					)}
				</CardContent>
			</Card>
		</div>
	);
}
