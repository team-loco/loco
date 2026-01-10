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
import { listTokens } from "@/gen/token/v1";
import { EntityType } from "@/gen/token/v1/token_pb";
import { toastConnectError } from "@/lib/error-handler";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";
import { ArrowRight } from "lucide-react";
import Loader from "@/assets/loader.svg?react";

export function Profile() {
	const { logout } = useAuth();
	const navigate = useNavigate();
	const { data: whoAmIResponse, isLoading } = useQuery(whoAmI, {});
	const user = whoAmIResponse?.user;

	// Fetch user's tokens
	const { data: tokensRes, isLoading: isTokensLoading } = useQuery(
		listTokens,
		user?.id ? { entityType: EntityType.USER, entityId: user.id } : undefined,
		{ enabled: !!user?.id }
	);
	const tokens = useMemo(() => tokensRes?.tokens ?? [], [tokensRes]);

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
			<Card>
				{/* Account Information */}
				<CardHeader>
					<CardTitle className="text-lg">Account Information</CardTitle>
				</CardHeader>
				<CardContent className="space-y-8 border-t border-border pt-6">
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

					{/* Tokens Section */}
					<div className="space-y-3 border-t border-border pt-6">
						<div className="flex items-center justify-between">
							<h3 className="font-semibold text-foreground">Tokens</h3>
							<Button size="sm" onClick={() => navigate("/tokens")}>
								Manage Tokens
								<ArrowRight className="h-4 w-4 ml-2" />
							</Button>
						</div>
						{isTokensLoading ? (
							<div className="flex items-center justify-center py-8">
								<Loader className="w-5 h-5" />
							</div>
						) : tokens.length === 0 ? (
							<p className="text-sm text-muted-foreground">
								No tokens yet. Create one on the tokens page.
							</p>
						) : (
							<div className="space-y-2">
								{tokens.slice(0, 3).map((token) => (
									<div
										key={`${token.name}-${token.entityType}-${token.entityId}`}
										className="flex items-center justify-between p-3 border border-border rounded-sm bg-muted/20"
									>
										<div className="flex-1 min-w-0">
											<p className="text-sm font-medium text-foreground truncate">
												{token.name}
											</p>
											<p className="text-xs text-muted-foreground">
												Expires{" "}
												{token.expiresAt
													? new Date(
															typeof token.expiresAt === "object" &&
															"seconds" in token.expiresAt
																? Number(
																		(token.expiresAt as Record<string, unknown>)
																			.seconds
																  ) * 1000
																: token.expiresAt
														  ).toLocaleDateString()
													: "never"}
											</p>
										</div>
									</div>
								))}
								{tokens.length > 3 && (
									<p className="text-xs text-muted-foreground text-center pt-2">
										+{tokens.length - 3} more token
										{tokens.length - 3 !== 1 ? "s" : ""}
									</p>
								)}
							</div>
						)}
					</div>

					{/* Account Management Section */}
					<div className="space-y-3 border-t border-border pt-6">
						<h3 className="font-semibold text-foreground">Settings</h3>
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
							<div className="space-y-2 p-4 border-2 border-error-border rounded-sm bg-error-bg">
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
					</div>
				</CardContent>
			</Card>
		</div>
	);
}
