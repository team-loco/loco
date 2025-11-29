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
import { deleteUser, getCurrentUser } from "@/gen/user/v1";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { toastConnectError } from "@/lib/error-handler";
import { toast } from "sonner";

export function Profile() {
	const { logout } = useAuth();
	const navigate = useNavigate();
	const { data: currentUserRes, isLoading } = useQuery(getCurrentUser, {});
	const user = currentUserRes?.user;

	const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
	const deleteUserMutation = useMutation(deleteUser);

	if (isLoading) {
		return <div>Loading...</div>;
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
		<div className="max-w-2xl mx-auto py-8">
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
							API token management coming in Phase 6
						</p>
						<Button variant="neutral" disabled>
							Create New Token
						</Button>
					</div>
				</CardContent>
			</Card>

			{/* Sessions & Security */}
			<Card>
				<CardHeader>
					<CardTitle className="text-lg">Sessions & Security</CardTitle>
				</CardHeader>
				<CardContent className="space-y-4">
					<div>
						<p className="text-sm text-foreground font-medium mb-2">
							Current Session
						</p>
						<p className="text-sm text-muted-foreground">
							Browser on {navigator.platform}
						</p>
					</div>

					<div className="space-y-2 pt-4 border-t border-border">
						<Button variant="neutral" className="w-full" disabled>
							Logout All Sessions
						</Button>

						{!showDeleteConfirm ? (
							<Button
								variant="neutral"
								className="w-full text-error-text border-error-border bg-error-bg hover:bg-error-bg/80"
								onClick={() => setShowDeleteConfirm(true)}
								disabled={deleteUserMutation.isPending}
							>
								Delete Account
							</Button>
						) : (
							<div className="space-y-2 p-4 border-2 border-error-border rounded-neo bg-error-bg">
								<p className="text-sm text-error-text font-medium">
									Are you sure? This action cannot be undone.
								</p>
								<div className="flex gap-2">
									<Button
										variant="neutral"
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
						<p className="text-xs text-muted-foreground text-center">
							Logout all sessions coming in Phase 6
						</p>
					</div>
				</CardContent>
			</Card>
		</div>
	);
}
