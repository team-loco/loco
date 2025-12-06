import { Button } from "@/components/ui/button";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { AppType, createApp } from "@/gen/app/v1";
import { checkDomainAvailability, DomainType, listActivePlatformDomains } from "@/gen/domain/v1";
import { getCurrentUserOrgs } from "@/gen/org/v1";
import { listWorkspaces } from "@/gen/workspace/v1";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { Check, Loader, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";
import { toast } from "sonner";

const APP_TYPES = [
	{ value: "SERVICE", label: "Service", description: "Backend service or API" },
	{ value: "DATABASE", label: "Database", description: "Managed database" },
	{ value: "FUNCTION", label: "Function", description: "Serverless function" },
	{ value: "CACHE", label: "Cache", description: "In-memory cache" },
	{ value: "QUEUE", label: "Queue", description: "Message queue" },
	{ value: "BLOB", label: "Blob Storage", description: "Object storage" },
];

export function CreateApp() {
	const navigate = useNavigate();
	const { workspaceId: paramWorkspaceId } = useParams();
	const [searchParams] = useSearchParams();
	const workspaceFromUrl = searchParams.get("workspace");

	const [appName, setAppName] = useState("");
	const [appType, setAppType] = useState("SERVICE");
	const [subdomain, setSubdomain] = useState("");
	const [selectedPlatformDomain, setSelectedPlatformDomain] = useState<string>("");
	const [subdomainAvailability, setSubdomainAvailability] = useState<
		"available" | "unavailable" | "checking" | null
	>(null);
	const debounceTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const hasUserEditedSubdomain = useRef(false);

	const { data: orgsRes } = useQuery(getCurrentUserOrgs, {});
	const orgs = orgsRes?.orgs ?? [];
	const firstOrgId = orgs.length > 0 ? orgs[0].id : null;

	const { data: workspacesRes } = useQuery(
		listWorkspaces,
		firstOrgId ? { orgId: firstOrgId } : undefined,
		{ enabled: !!firstOrgId }
	);
	const workspaces = workspacesRes?.workspaces ?? [];
	const workspaceId =
		paramWorkspaceId || (workspaces.length > 0 ? workspaces[0].id : null);

	const { data: platformDomainsRes } = useQuery(listActivePlatformDomains, {});
	const platformDomains = platformDomainsRes?.platformDomains ?? [];

	const createAppMutation = useMutation(createApp);
	const checkSubdomainMutation = useMutation(checkDomainAvailability);

	// Set default platform domain on load
	useEffect(() => {
		if (platformDomains.length > 0 && !selectedPlatformDomain) {
			setSelectedPlatformDomain(platformDomains[0].domain);
		}
	}, [platformDomains, selectedPlatformDomain]);

	// Auto-fill subdomain from app name (only if user hasn't manually edited it)
	useEffect(() => {
		if (hasUserEditedSubdomain.current) return;

		const sanitized = appName
			.toLowerCase()
			.replace(/[^a-z0-9-]/g, "")
			.replace(/^-+|-+$/g, "");
		setSubdomain(sanitized);
	}, [appName]);

	// Debounced subdomain availability check
	useEffect(() => {
		if (!subdomain.trim() || !selectedPlatformDomain) {
			setSubdomainAvailability(null);
			return;
		}

		if (debounceTimer.current) {
			clearTimeout(debounceTimer.current);
		}

		setSubdomainAvailability("checking");
		debounceTimer.current = setTimeout(async () => {
			try {
				const fullDomain = `${subdomain}.${selectedPlatformDomain}`;
				const res = await checkSubdomainMutation.mutateAsync({
					domain: fullDomain,
				});
				setSubdomainAvailability(res.isAvailable ? "available" : "unavailable");
			} catch {
				setSubdomainAvailability("unavailable");
			}
		}, 500);

		return () => {
			if (debounceTimer.current) {
				clearTimeout(debounceTimer.current);
			}
		};
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [subdomain, selectedPlatformDomain]);

	const handleSubmit = async (e: React.FormEvent) => {
		e.preventDefault();

		if (!appName.trim()) {
			toast.error("App name is required");
			return;
		}

		if (!workspaceId) {
			toast.error("No workspace available");
			return;
		}

		if (subdomainAvailability === "unavailable") {
			toast.error("The chosen subdomain is not available");
			return;
		}

		try {
			const platformDomain = platformDomains.find(
				(d) => d.domain === selectedPlatformDomain
			);
			const fullDomain = `${subdomain}.${selectedPlatformDomain}`;

			const res = await createAppMutation.mutateAsync({
				name: appName,
				workspaceId:
					typeof workspaceId === "string" ? BigInt(workspaceId) : workspaceId,
				type: AppType[appType as keyof typeof AppType],
				domain: {
					domain: fullDomain,
					domainSource: DomainType.PLATFORM_PROVIDED,
					platformDomainId: platformDomain?.id || BigInt(0),
				},
			});

			if (res.app?.id) {
				toast.success("App created successfully");
				navigate(
					`/app/${res.app.id}${
						workspaceFromUrl ? `?workspace=${workspaceFromUrl}` : ""
					}`
				);
			} else {
				toast.error("Failed to create app");
			}
		} catch (error) {
			const message =
				error instanceof Error ? error.message : "Failed to create app";
			toast.error(message);
		}
	};

	return (
		<div className="max-w-2xl mx-auto py-8">
			<div className="mb-8">
				<h1 className="text-3xl font-heading text-foreground mb-2">
					Create App
				</h1>
				<p className="text-muted-foreground">
					Set up a new application or service
				</p>
			</div>

			<form onSubmit={handleSubmit} className="space-y-6">
				{/* App Name */}
				<Card>
					<CardHeader>
						<CardTitle className="text-lg">App Name</CardTitle>
					</CardHeader>
					<CardContent>
						<Label htmlFor="app-name" className="text-sm mb-2 block">
							Name
						</Label>
						<Input
							id="app-name"
							placeholder="e.g., API v2, Database, Worker"
							value={appName}
							onChange={(e) => setAppName(e.target.value)}
							className="border-border"
						/>
					</CardContent>
				</Card>

				{/* App Type */}
				<Card>
					<CardHeader>
						<CardTitle className="text-lg">App Type</CardTitle>
						<CardDescription>
							Choose what kind of app you're deploying
						</CardDescription>
					</CardHeader>
					<CardContent>
						<div className="grid grid-cols-2 gap-3">
							{APP_TYPES.map((type) => (
								<button
									key={type.value}
									type="button"
									onClick={() => setAppType(type.value)}
									className={`p-4 rounded-neo border-2 text-left transition-all ${
										appType === type.value
											? "border-main bg-main/5"
											: "border-border hover:border-main/50"
									}`}
								>
									<div className="font-medium text-sm text-foreground">
										{type.label}
									</div>
									<div className="text-xs text-muted-foreground mt-1">
										{type.description}
									</div>
								</button>
							))}
						</div>
					</CardContent>
				</Card>

				{/* Platform Domain & Subdomain */}
				<Card>
					<CardHeader>
						<CardTitle className="text-lg">Domain</CardTitle>
						<CardDescription>Your app's URL</CardDescription>
					</CardHeader>
					<CardContent className="space-y-4">
						{/* Platform Domain Selection */}
						<div className="space-y-2">
							<Label htmlFor="platform-domain" className="text-sm">
								Platform Domain
							</Label>
							<Select value={selectedPlatformDomain} onValueChange={setSelectedPlatformDomain}>
								<SelectTrigger id="platform-domain" className="border-border">
									<SelectValue placeholder="Select a platform domain" />
								</SelectTrigger>
								<SelectContent>
									{platformDomains.map((domain) => (
										<SelectItem key={domain.id} value={domain.domain}>
											{domain.domain}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>

						{/* Subdomain Input */}
						<div className="space-y-2">
							<Label htmlFor="subdomain" className="text-sm">
								Subdomain
							</Label>
							<div className="flex gap-2">
								<Input
									id="subdomain"
									placeholder="my-app"
									value={subdomain}
									onChange={(e) => {
										hasUserEditedSubdomain.current = true;
										setSubdomain(
											e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "")
										);
									}}
									className="border-border flex-1"
								/>
								<div className="flex items-center px-3 bg-secondary rounded-neo border border-border text-sm text-muted-foreground shrink-0">
									.{selectedPlatformDomain}
								</div>
								{subdomainAvailability && (
									<div className="flex items-center px-3">
										{subdomainAvailability === "checking" && (
											<Loader className="h-4 w-4 animate-spin text-foreground" />
										)}
										{subdomainAvailability === "available" && (
											<Check className="h-4 w-4 text-success-text" />
										)}
										{subdomainAvailability === "unavailable" && (
											<X className="h-4 w-4 text-error-text" />
										)}
									</div>
								)}
							</div>
							{subdomainAvailability === "unavailable" && (
								<p className="text-xs text-error-text">
									This domain is not available
								</p>
							)}
							{subdomainAvailability === "available" && (
								<p className="text-xs text-success-text">
									This domain is available
								</p>
							)}
						</div>
					</CardContent>
				</Card>

				{/* Actions */}
				<div className="flex gap-3 justify-end">
					<Button
						type="button"
						variant="neutral"
						onClick={() => navigate("/dashboard")}
					>
						Cancel
					</Button>
					<Button
						type="submit"
						disabled={
							!!(
								createAppMutation.isPending ||
								!appName.trim() ||
								(subdomain.trim() !== "" &&
									(subdomainAvailability === "unavailable" ||
										subdomainAvailability === "checking"))
							)
						}
					>
						{createAppMutation.isPending ? "Creating..." : "Create App"}
					</Button>
				</div>
			</form>
		</div>
	);
}
