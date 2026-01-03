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
import { Slider } from "@/components/ui/slider";
import Loader from "@/assets/loader.svg?react";
import {
	ResourceStatus,
	deleteResource,
	getResource,
	scaleResource,
	updateResource,
} from "@/gen/resource/v1";
import {
	addResourceDomain,
	checkDomainAvailability,
	listActivePlatformDomains,
	removeResourceDomain,
	setPrimaryResourceDomain,
	updateResourceDomain,
} from "@/gen/domain/v1";
import type { ResourceDomain } from "@/gen/domain/v1/domain_pb";
import { DomainType } from "@/gen/domain/v1/domain_pb";
import { toastConnectError } from "@/lib/error-handler";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { Cpu, HardDrive } from "lucide-react";
import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { toast } from "sonner";

export function ResourceSettings() {
	const { resourceId } = useParams<{ resourceId: string }>();
	const navigate = useNavigate();

	const {
		data: resourceRes,
		isLoading,
		refetch,
	} = useQuery(getResource, resourceId ? { resourceId: BigInt(resourceId) } : undefined, {
		enabled: !!resourceId,
	});
	const resource = resourceRes?.resource;

	const [name, setName] = useState(resource?.name || "");
	const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
	const [newDomain, setNewDomain] = useState("");
	const [domainSource, setDomainSource] = useState<"platform" | "user">(
		"platform"
	);
	const [platformDomainId, setPlatformDomainId] = useState<string>("");
	const [editingDomainId, setEditingDomainId] = useState<bigint | null>(null);
	const [editDomainValue, setEditDomainValue] = useState("");
	const [cpuValue, setCpuValue] = useState<number[]>([500]);
	const [memoryValue, setMemoryValue] = useState<number[]>([512]);

	const { data: platformDomainsRes } = useQuery(listActivePlatformDomains, {});
	const platformDomains = platformDomainsRes?.platformDomains || [];

	const updateResourceMutation = useMutation(updateResource);
	const deleteResourceMutation = useMutation(deleteResource);
	const addDomainMutation = useMutation(addResourceDomain);
	const removeDomainMutation = useMutation(removeResourceDomain);
	const setPrimaryMutation = useMutation(setPrimaryResourceDomain);
	const checkSubdomainMutation = useMutation(checkDomainAvailability);
	const updateDomainMutation = useMutation(updateResourceDomain);
	const scaleResourceMutation = useMutation(scaleResource);

	const hasChanges = name !== resource?.name;

	const handleSave = async () => {
		if (!resourceId) return;
		try {
			await updateResourceMutation.mutateAsync({
				resourceId: BigInt(resourceId),
				name: name || resource?.name || "",
			});
			toast.success("Resource updated successfully");
		} catch (error) {
			toastConnectError(error);
			console.error("Failed to update resource:", error);
		}
	};

	const handleDelete = async () => {
		if (!resourceId) return;
		try {
			await deleteResourceMutation.mutateAsync({ resourceId: BigInt(resourceId) });
			toast.success("Resource deleted successfully");
			navigate("/dashboard");
		} catch (error) {
			toastConnectError(error);
			console.error("Failed to delete resource:", error);
		}
	};

	const handleAddDomain = async () => {
		if (!resourceId || !newDomain) return;
		try {
			const domainInput = {
				domainSource:
					domainSource === "platform"
						? DomainType.PLATFORM_PROVIDED
						: DomainType.USER_PROVIDED,
				subdomain: domainSource === "platform" ? newDomain : undefined,
				platformDomainId:
					domainSource === "platform" && platformDomainId
						? BigInt(platformDomainId)
						: undefined,
				domain: domainSource === "user" ? newDomain : undefined,
			};

			await addDomainMutation.mutateAsync({
				resourceId: BigInt(resourceId),
				domain: domainInput,
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
		if (!resourceId) return;
		try {
			await setPrimaryMutation.mutateAsync({
				resourceId: BigInt(resourceId),
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

	const handleEditDomain = (domain: ResourceDomain) => {
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
				console.log("hit this piece");
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

	const handleScale = async () => {
		if (!resourceId) return;
		try {
			await scaleResourceMutation.mutateAsync({
				resourceId: BigInt(resourceId),
				cpu: `${cpuValue[0]}m`,
				memory: `${memoryValue[0]}Mi`,
			});
			toast.success("Resource scaling initiated");
			await refetch();
		} catch (error) {
			toastConnectError(error);
			console.error("Failed to scale resource:", error);
		}
	};

	if (isLoading) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<div className="text-center">
					<div className="flex flex-col gap-2 items-center">
						<Loader className="w-8 h-8" />
						<p className="text-foreground font-base">Loading...</p>
					</div>
				</div>
			</div>
		);
	}

	if (!resource) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-heading mb-2">Resource Not Found</p>
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
					Manage your resource settings
				</p>
			</div>

			{/* Resource Info */}
			<Card className="border-2">
				<CardHeader>
					<CardTitle>Resource Information</CardTitle>
				</CardHeader>
				<CardContent className="space-y-4">
					<div>
						<label className="text-sm font-medium text-foreground">
							Resource Name
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
							disabled={!hasChanges || updateResourceMutation.isPending}
						>
							{updateResourceMutation.isPending ? (
								<>
									<Loader className="w-4 h-4 mr-2" />
									Saving...
								</>
							) : (
								"Save Changes"
							)}
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
							{addDomainMutation.isPending ? (
								<>
									<Loader className="w-4 h-4 mr-2" />
									Adding...
								</>
							) : (
								"Add Domain"
							)}
						</Button>
					</div>
				</CardContent>
			</Card>

			{/* Scaling */}
			<Card
				className={`border-2 ${
					resource?.status === ResourceStatus.UNAVAILABLE ? "opacity-50" : ""
				}`}
			>
				<CardHeader>
					<CardTitle>Resource Scaling</CardTitle>
				</CardHeader>
				<CardContent
					className={`space-y-6 ${
						resource?.status === ResourceStatus.UNAVAILABLE ? "pointer-events-none" : ""
					}`}
				>
					{resource?.status === ResourceStatus.UNAVAILABLE && (
						<div className="bg-yellow-50 dark:bg-yellow-950/20 border border-yellow-200 dark:border-yellow-900 rounded-lg p-3 mb-4">
							<p className="text-sm text-yellow-700 dark:text-yellow-400">
								Deploy your resource before scaling
							</p>
						</div>
					)}
					{/* CPU */}
					<div>
						<div className="flex items-center gap-2 mb-3">
							<Cpu className="h-5 w-5 text-blue-500" />
							<div>
								<label className="text-sm font-medium text-foreground">
									CPU Resources
								</label>
								<p className="text-xs text-foreground/70">{cpuValue[0]}m</p>
							</div>
						</div>
						<Slider
							value={cpuValue}
							onValueChange={setCpuValue}
							min={100}
							max={4000}
							step={100}
							className="w-full"
							disabled={resource?.status === ResourceStatus.UNAVAILABLE}
							/>
							<p className="text-xs text-foreground/50 mt-2">
							Range: 100m - 4000m
							</p>
							</div>

							{/* Memory */}
							<div>
							<div className="flex items-center gap-2 mb-3">
							<HardDrive className="h-5 w-5 text-amber-500" />
							<div>
								<label className="text-sm font-medium text-foreground">
									Memory Resources
								</label>
								<p className="text-xs text-foreground/70">{memoryValue[0]}Mi</p>
							</div>
							</div>
							<Slider
							value={memoryValue}
							onValueChange={setMemoryValue}
							min={128}
							max={8192}
							step={128}
							className="w-full"
							disabled={resource?.status === ResourceStatus.UNAVAILABLE}
						/>
						<p className="text-xs text-foreground/50 mt-2">
							Range: 128Mi - 8192Mi
						</p>
					</div>

					<Button
						onClick={handleScale}
						disabled={
							scaleResourceMutation.isPending || resource?.status === ResourceStatus.UNAVAILABLE
						}
						className="w-full"
					>
						{scaleResourceMutation.isPending ? "Scaling..." : "Apply Scaling"}
					</Button>
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
								Delete Resource
							</h3>
							<p className="text-sm text-foreground opacity-70 mb-4">
								This action cannot be undone. All data associated with this resource
								will be permanently deleted.
							</p>
						</div>

						<Button
							className="text-red-700 dark:text-red-400 border-red-200 dark:border-red-900 hover:bg-red-100 dark:hover:bg-red-950"
							variant="outline"
							onClick={() => setShowDeleteConfirm(true)}
						>
							Delete Resource
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
						Deleting <strong>{resource.name}</strong> will permanently remove all
						data associated with this resource.
					</p>
					<div className="flex gap-2 pt-2">
						<Button
							variant="secondary"
							className="flex-1 text-sm"
							onClick={() => setShowDeleteConfirm(false)}
							disabled={deleteResourceMutation.isPending}
						>
							Cancel
						</Button>
						<Button
							className="flex-1 text-sm text-red-700 dark:text-red-400 border-red-200 dark:border-red-900 hover:bg-red-100 dark:hover:bg-red-950"
							variant="outline"
							onClick={handleDelete}
							disabled={deleteResourceMutation.isPending}
						>
							{deleteResourceMutation.isPending ? "Deleting..." : "Delete"}
						</Button>
					</div>
				</div>
			)}
		</div>
	);
}
