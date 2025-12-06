import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { deleteApp, getApp, updateApp } from "@/gen/app/v1";
import {
	addAppDomain,
	checkDomainAvailability,
	listActivePlatformDomains,
	removeAppDomain,
	setPrimaryAppDomain,
	updateAppDomain,
} from "@/gen/domain/v1";
import type { AppDomain } from "@/gen/domain/v1/domain_pb";
import { DomainType } from "@/gen/domain/v1/domain_pb";
import { toastConnectError } from "@/lib/error-handler";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { toast } from "sonner";

export function AppSettings() {
	const { appId } = useParams<{ appId: string }>();
	const navigate = useNavigate();

	const {
		data: appRes,
		isLoading,
		refetch,
	} = useQuery(getApp, appId ? { appId: BigInt(appId) } : undefined, {
		enabled: !!appId,
	});
	const app = appRes?.app;

	const [name, setName] = useState(app?.name || "");
	const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
	const [newDomain, setNewDomain] = useState("");
	const [domainSource, setDomainSource] = useState<"platform" | "user">(
		"platform"
	);
	const [platformDomainId, setPlatformDomainId] = useState<string>("");
	const [editingDomainId, setEditingDomainId] = useState<bigint | null>(null);
	const [editDomainValue, setEditDomainValue] = useState("");

	const { data: platformDomainsRes } = useQuery(listActivePlatformDomains, {});
	const platformDomains = platformDomainsRes?.platformDomains || [];

	const updateAppMutation = useMutation(updateApp);
	const deleteAppMutation = useMutation(deleteApp);
	const addDomainMutation = useMutation(addAppDomain);
	const removeDomainMutation = useMutation(removeAppDomain);
	const setPrimaryMutation = useMutation(setPrimaryAppDomain);
	const checkSubdomainMutation = useMutation(checkDomainAvailability);
	const updateDomainMutation = useMutation(updateAppDomain);

	const hasChanges = name !== app?.name;

	const handleSave = async () => {
		if (!appId) return;
		try {
			await updateAppMutation.mutateAsync({
				appId: BigInt(appId),
				name: name || app?.name || "",
			});
			toast.success("App updated successfully");
		} catch (error) {
			toastConnectError(error);
			console.error("Failed to update app:", error);
		}
	};

	const handleDelete = async () => {
		if (!appId) return;
		try {
			await deleteAppMutation.mutateAsync({ appId: BigInt(appId) });
			toast.success("App deleted successfully");
			navigate("/dashboard");
		} catch (error) {
			toastConnectError(error);
			console.error("Failed to delete app:", error);
		}
	};

	const handleAddDomain = async () => {
		if (!appId || !newDomain) return;
		try {
			await addDomainMutation.mutateAsync({
				appId: BigInt(appId),
				domain: newDomain,
				domainSource:
					domainSource === "platform"
						? DomainType.PLATFORM_PROVIDED
						: DomainType.USER_PROVIDED,
				platformDomainId:
					domainSource === "platform" && platformDomainId
						? BigInt(platformDomainId)
						: undefined,
			});
			toast.success("Domain added successfully");
			setNewDomain("");
			setPlatformDomainId("");
			await refetch();
		} catch (error) {
			toastConnectError(error);
			console.error("Failed to add domain:", error);
		}
	};

	const handleSetPrimary = async (domainId: bigint) => {
		if (!appId) return;
		try {
			await setPrimaryMutation.mutateAsync({
				appId: BigInt(appId),
				domainId,
			});
			toast.success("Primary domain updated");
			await refetch();
		} catch (error) {
			toastConnectError(error);
			console.error("Failed to set primary domain:", error);
		}
	};

	const handleRemoveDomain = async (domainId: bigint) => {
		try {
			await removeDomainMutation.mutateAsync({ domainId });
			toast.success("Domain removed successfully");
			await refetch();
		} catch (error) {
			toastConnectError(error);
			console.error("Failed to remove domain:", error);
		}
	};

	const handleEditDomain = (domain: AppDomain) => {
		setEditingDomainId(domain.id);
		setEditDomainValue(domain.domain);
	};

	const handleSaveDomainEdit = async () => {
		if (!editingDomainId || !editDomainValue.trim()) {
			return;
		}

		try {
			// Check if domain is available
			const res = await checkSubdomainMutation.mutateAsync({
				domain: editDomainValue,
			});

			if (!res.isAvailable) {
				toast.error("This domain is already in use");
				return;
			}

			// Update the domain
			const resp = await updateDomainMutation.mutateAsync({
				domainId: editingDomainId,
				domain: editDomainValue,
			});

			toast.success(resp.message);
			setEditingDomainId(null);
			setEditDomainValue("");
			await refetch();
		} catch (error) {
			toastConnectError(error);
			console.error("Failed to update domain:", error);
		}
	};

	const handleCancelEdit = () => {
		setEditingDomainId(null);
		setEditDomainValue("");
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
		<div className="space-y-6">
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

					<div className="flex gap-2 pt-4">
						<Button
							variant="outline"
							onClick={() => {
								setName(app.name);
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

			{/* Domain Management */}
			<Card className="border-2">
				<CardHeader>
					<CardTitle>Domain Management</CardTitle>
				</CardHeader>
				<CardContent className="space-y-4">
					{/* Current Domains */}
					{app?.domains && app.domains.length > 0 && (
						<div className="space-y-3 pb-4 border-b">
							<div className="text-sm font-medium text-foreground">
								Current Domains
							</div>
							<div className="bg-background/50 rounded-lg p-3 space-y-2">
								{app.domains.map((domain) => (
									<div key={domain.id}>
										{editingDomainId === domain.id ? (
											<div className="flex items-center justify-between gap-2">
												<Input
													value={editDomainValue}
													onChange={(e) => setEditDomainValue(e.target.value)}
													className="flex-1"
													placeholder="Enter domain"
												/>
												<Button
													size="sm"
													className="text-xs shrink-0"
													onClick={handleSaveDomainEdit}
													disabled={!editDomainValue.trim()}
												>
													Save
												</Button>
												<Button
													variant="outline"
													size="sm"
													className="text-xs border-2 shrink-0"
													onClick={handleCancelEdit}
												>
													Cancel
												</Button>
											</div>
										) : (
											<div className="flex items-center justify-between gap-2">
												<div className="flex-1">
													<div className="font-mono text-sm break-all">
														{domain.domain}
													</div>
													{domain.isPrimary && (
														<div className="text-xs text-foreground/70 mt-1">
															Primary Domain
														</div>
													)}
												</div>
												<Button
													variant="outline"
													size="sm"
													className="text-xs border-2 shrink-0"
													onClick={() => handleEditDomain(domain)}
												>
													Edit
												</Button>
												{!domain.isPrimary && (
													<Button
														variant="outline"
														size="sm"
														className="text-xs border-2 shrink-0"
														onClick={() =>
															handleSetPrimary(BigInt(domain.id || 0))
														}
														disabled={setPrimaryMutation.isPending}
													>
														Set Primary
													</Button>
												)}
												<Button
													variant="outline"
													size="sm"
													className="text-xs border-2 border-error-border text-error-text shrink-0"
													onClick={() =>
														handleRemoveDomain(BigInt(domain.id || 0))
													}
													disabled={
														removeDomainMutation.isPending || domain.isPrimary
													}
													title={
														domain.isPrimary
															? "Cannot remove primary domain"
															: ""
													}
												>
													Remove
												</Button>
											</div>
										)}
									</div>
								))}
							</div>
						</div>
					)}

					{/* Add New Domain */}
					<div className="space-y-3">
						<div className="text-sm font-medium text-foreground">
							Add Domain
						</div>

						<div>
							<label className="text-xs font-medium text-foreground mb-2 block">
								Domain Type
							</label>
							<Select
								value={domainSource}
								onValueChange={(value) =>
									setDomainSource(value as "platform" | "user")
								}
							>
								<SelectTrigger>
									<SelectValue />
								</SelectTrigger>
								<SelectContent>
									<SelectItem value="platform">Platform Domain</SelectItem>
									<SelectItem value="user">User-Provided Domain</SelectItem>
								</SelectContent>
							</Select>
						</div>

						{domainSource === "platform" && (
							<div>
								<label className="text-xs font-medium text-foreground mb-2 block">
									Select Base Domain
								</label>
								<Select
									value={platformDomainId}
									onValueChange={setPlatformDomainId}
								>
									<SelectTrigger>
										<SelectValue placeholder="Choose a domain..." />
									</SelectTrigger>
									<SelectContent>
										{platformDomains.map((pd) => (
											<SelectItem key={pd.id} value={pd.id.toString()}>
												{pd.domain}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
								<p className="text-xs text-foreground/70 mt-2">
									Enter subdomain prefix in the field below
								</p>
							</div>
						)}

						<div>
							<label className="text-xs font-medium text-foreground">
								{domainSource === "platform"
									? "Subdomain / Full Domain"
									: "Full Domain"}
							</label>
							<Input
								value={newDomain}
								onChange={(e) => setNewDomain(e.target.value)}
								placeholder={
									domainSource === "platform" ? "myapp.loco.dev" : "example.com"
								}
								className="mt-1"
							/>
						</div>

						<Button
							onClick={handleAddDomain}
							disabled={
								!newDomain ||
								(domainSource === "platform" && !platformDomainId) ||
								addDomainMutation.isPending
							}
						>
							{addDomainMutation.isPending ? "Adding..." : "Add Domain"}
						</Button>
					</div>
				</CardContent>
			</Card>

			{/* Danger Zone */}
			<Card className="border-2 border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/20">
				<CardHeader>
					<CardTitle className="text-red-700 dark:text-red-400">
						Danger Zone
					</CardTitle>
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

						<Button
							className="text-red-700 dark:text-red-400 border-red-200 dark:border-red-900 hover:bg-red-100 dark:hover:bg-red-950"
							variant="outline"
							onClick={() => setShowDeleteConfirm(true)}
						>
							Delete Application
						</Button>
					</div>
				</CardContent>
			</Card>

			{/* Delete Confirmation Dialog */}
			{showDeleteConfirm && (
				<div className="space-y-2 p-4 border-2 border-red-200 dark:border-red-900 rounded-lg bg-red-50 dark:bg-red-950/20">
					<p className="text-sm text-red-700 dark:text-red-400 font-medium">
						Are you sure? This action cannot be undone.
					</p>
					<p className="text-xs text-red-600 dark:text-red-500 opacity-90">
						Deleting <strong>{app.name}</strong> will permanently remove all
						data associated with this application.
					</p>
					<div className="flex gap-2 pt-2">
						<Button
							variant="secondary"
							className="flex-1 text-sm"
							onClick={() => setShowDeleteConfirm(false)}
							disabled={deleteAppMutation.isPending}
						>
							Cancel
						</Button>
						<Button
							className="flex-1 text-sm text-red-700 dark:text-red-400 border-red-200 dark:border-red-900 hover:bg-red-100 dark:hover:bg-red-950"
							variant="outline"
							onClick={handleDelete}
							disabled={deleteAppMutation.isPending}
						>
							{deleteAppMutation.isPending ? "Deleting..." : "Delete"}
						</Button>
					</div>
				</div>
			)}
		</div>
	);
}
