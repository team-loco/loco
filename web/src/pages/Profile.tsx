import { useQuery } from "@connectrpc/connect-query";
import { getCurrentUser } from "@/gen/user/v1";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useState } from "react";
import { toast } from "sonner";

export function Profile() {
	const { data: currentUserRes, isLoading } = useQuery(getCurrentUser, {});
	const user = currentUserRes?.user;

	const [isEditing, setIsEditing] = useState(false);
	const [name, setName] = useState(user?.name ?? "");

	if (isLoading) {
		return <div>Loading...</div>;
	}

	if (!user) {
		return <div>User not found</div>;
	}

	const handleSave = () => {
		toast.success("Profile updated");
		setIsEditing(false);
	};

	return (
		<div className="max-w-2xl mx-auto py-8">
			<div className="mb-8">
				<h1 className="text-3xl font-heading font-bold text-foreground mb-2">
					Profile Settings
				</h1>
				<p className="text-muted-foreground">
					Manage your account and preferences
				</p>
			</div>

			{/* Profile Info */}
			<Card className="mb-6">
				<CardHeader>
					<CardTitle className="text-lg">Account Information</CardTitle>
				</CardHeader>
				<CardContent className="space-y-6">
					{/* Avatar */}
					<div>
						<Label className="text-sm mb-3 block">Avatar</Label>
						<div className="flex items-center gap-4">
							<div className="w-16 h-16 bg-main rounded-neo flex items-center justify-center text-white font-heading text-2xl font-bold">
								{user.name.charAt(0).toUpperCase()}
							</div>
							<Button variant="neutral" size="sm" disabled>
								Upload Photo
							</Button>
							<p className="text-xs text-muted-foreground">
								Coming soon
							</p>
						</div>
					</div>

					{/* Name */}
					<div>
						<Label htmlFor="name" className="text-sm mb-2 block">
							Name
						</Label>
						<Input
							id="name"
							value={isEditing ? name : user.name}
							onChange={(e) => setName(e.target.value)}
							disabled={!isEditing}
							className="border-border"
						/>
					</div>

					{/* Email */}
					<div>
						<Label htmlFor="email" className="text-sm mb-2 block">
							Email
						</Label>
						<Input
							id="email"
							value={user.email}
							disabled
							className="border-border bg-secondary"
						/>
						<p className="text-xs text-muted-foreground mt-2">
							Read-only. Managed by your OAuth provider.
						</p>
					</div>

					{/* Actions */}
					<div className="flex gap-3 pt-4">
						{!isEditing ? (
							<Button
								variant="neutral"
								onClick={() => setIsEditing(true)}
							>
								Edit Profile
							</Button>
						) : (
							<>
								<Button
									variant="neutral"
									onClick={() => {
										setIsEditing(false);
										setName(user.name);
									}}
								>
									Cancel
								</Button>
								<Button onClick={handleSave}>
									Save Changes
								</Button>
							</>
						)}
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
						<Button
							variant="neutral"
							className="w-full text-red-600 border-red-200 hover:bg-red-50"
							disabled
						>
							Delete Account
						</Button>
						<p className="text-xs text-muted-foreground text-center">
							These actions coming in Phase 6
						</p>
					</div>
				</CardContent>
			</Card>
		</div>
	);
}
