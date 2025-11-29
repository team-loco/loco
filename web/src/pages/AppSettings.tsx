import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { deleteApp, getApp, updateApp } from "@/gen/app/v1";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useState } from "react";
import { useNavigate, useParams } from "react-router";

export function AppSettings() {
	const { appId } = useParams<{ appId: string }>();
	const navigate = useNavigate();

	const { data: appRes, isLoading } = useQuery(
		getApp,
		appId ? { appId: BigInt(appId) } : undefined,
		{ enabled: !!appId }
	);
	const app = appRes?.app;

	const [name, setName] = useState(app?.name || "");
	const [subdomain, setSubdomain] = useState(app?.subdomain || "");
	const [domain, setDomain] = useState(app?.domain || "");
	const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

	const updateAppMutation = useMutation(updateApp);
	const deleteAppMutation = useMutation(deleteApp);

	const hasChanges =
		name !== app?.name ||
		subdomain !== app?.subdomain ||
		domain !== app?.domain;

	const handleSave = async () => {
		if (!appId) return;
		try {
			await updateAppMutation.mutateAsync({
				appId: BigInt(appId),
				name: name || app?.name || "",
				subdomain: subdomain || app?.subdomain || "",
				domain: domain || app?.domain || "",
			});
		} catch (error) {
			console.error("Failed to update app:", error);
		}
	};

	const handleDelete = async () => {
		if (!appId) return;
		try {
			await deleteAppMutation.mutateAsync({ appId: BigInt(appId) });
			navigate("/dashboard");
		} catch (error) {
			console.error("Failed to delete app:", error);
		}
	};

	if (isLoading) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<div className="text-center">
					<div className="inline-flex gap-2 items-center">
						<div className="w-4 h-4 bg-main rounded-full animate-pulse"></div>
						<p className="text-foreground font-base">Loading...</p>
					</div>
				</div>
			</div>
		);
	}

	if (!app) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-heading mb-2">App Not Found</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	return (
		<div className="space-y-6 max-w-2xl">
			<div className="space-y-1">
				<h1 className="text-3xl font-heading text-foreground">Settings</h1>
				<p className="text-sm text-foreground opacity-70">
					Manage your application settings
				</p>
			</div>

			{/* App Info */}
			<Card className="border-2">
				<CardHeader>
					<CardTitle>App Information</CardTitle>
				</CardHeader>
				<CardContent className="space-y-4">
					<div>
						<label className="text-sm font-medium text-foreground">
							App Name
						</label>
						<Input
							value={name}
							onChange={(e) => setName(e.target.value)}
							className="mt-1"
						/>
					</div>

					<div>
						<label className="text-sm font-medium text-foreground">
							Subdomain
						</label>
						<div className="flex gap-2 mt-1">
							<Input
								value={subdomain}
								onChange={(e) => setSubdomain(e.target.value)}
								placeholder="my-app"
							/>
							<span className="text-sm text-foreground opacity-70 py-2">
								.deploy-app.com
							</span>
						</div>
					</div>

					<div>
						<label className="text-sm font-medium text-foreground">
							Custom Domain (Optional)
						</label>
						<Input
							value={domain}
							onChange={(e) => setDomain(e.target.value)}
							placeholder="example.com"
							className="mt-1"
						/>
						<p className="text-xs text-foreground opacity-50 mt-1">
							Point your domain with a CNAME record to your subdomain
						</p>
					</div>

					<div className="flex gap-2 pt-4">
						<Button
							variant="noShadow"
							onClick={() => {
								setName(app.name);
								setSubdomain(app.subdomain);
								setDomain(app.domain);
							}}
							className="border-2"
							disabled={!hasChanges}
						>
							Reset
						</Button>
						<Button
							onClick={handleSave}
							disabled={!hasChanges || updateAppMutation.isPending}
						>
							{updateAppMutation.isPending ? "Saving..." : "Save Changes"}
						</Button>
					</div>
				</CardContent>
			</Card>

			{/* Danger Zone */}
			<Card className="border-2 border-red-200">
				<CardHeader>
					<CardTitle className="text-destructive">Danger Zone</CardTitle>
				</CardHeader>
				<CardContent>
					<div className="space-y-4">
						<div>
							<h3 className="font-medium text-foreground mb-2">
								Delete Application
							</h3>
							<p className="text-sm text-foreground opacity-70 mb-4">
								This action cannot be undone. All data associated with this app
								will be permanently deleted.
							</p>
						</div>

						<Button onClick={() => setShowDeleteConfirm(true)}>
							Delete Application
						</Button>
					</div>
				</CardContent>
			</Card>

			{/* Delete Confirmation Dialog */}
			{showDeleteConfirm && (
				<div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center">
					<Card className="max-w-sm">
						<CardHeader>
							<CardTitle>Delete Application</CardTitle>
						</CardHeader>
						<CardContent className="space-y-4">
							<div>
								<p className="text-sm text-foreground opacity-70 mb-4">
									Are you sure you want to delete <strong>{app.name}</strong>?
									This action cannot be undone.
								</p>
								<p className="text-xs text-foreground opacity-50">
									Type the app name to confirm deletion:
								</p>
							</div>

							<Input placeholder={app.name} className="mt-2" disabled />

							<div className="flex gap-2 pt-4">
								<Button
									variant="noShadow"
									onClick={() => setShowDeleteConfirm(false)}
									className="border-2 flex-1"
									disabled={deleteAppMutation.isPending}
								>
									Cancel
								</Button>
								<Button
									onClick={handleDelete}
									disabled={deleteAppMutation.isPending}
									className="flex-1"
								>
									{deleteAppMutation.isPending ? "Deleting..." : "Delete"}
								</Button>
							</div>
						</CardContent>
					</Card>
				</div>
			)}
		</div>
	);
}
