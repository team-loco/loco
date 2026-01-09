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
import { Slider } from "@/components/ui/slider";
import { Textarea } from "@/components/ui/textarea";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import {
	ResourceType,
	createResource,
	RoutingConfigSchema,
	LoggingConfigSchema,
	MetricsConfigSchema,
	TracingConfigSchema,
	RegionTargetSchema,
	ServiceSpecSchema,
	ResourceSpecSchema,
} from "@/gen/resource/v1";
import { create } from "@bufbuild/protobuf";
import {
	DomainType,
	listPlatformDomains,
	checkDomainAvailability,
} from "@/gen/domain/v1";
import { listUserOrgs } from "@/gen/org/v1";
import { listOrgWorkspaces } from "@/gen/workspace/v1";
import { createDeployment } from "@/gen/deployment/v1";
import { getErrorMessage, toastConnectError } from "@/lib/error-handler";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useAuth } from "@/auth/AuthProvider";
import {
	Check,
	Loader,
	X,
	Server,
	Database,
	Zap,
	Layers,
	Mail,
	HardDrive,
	Plus,
	Trash2,
	FileText,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";
import { toast } from "sonner";

const REGIONS = [
	{ value: "us-east-1", label: "US East (N. Virginia)" },
	{ value: "us-west-2", label: "US West (Oregon)" },
	{ value: "eu-west-1", label: "EU West (Ireland)" },
	{ value: "ap-southeast-1", label: "Asia Pacific (Singapore)" },
];

const CPU_OPTIONS = ["0.25", "0.5", "1", "2", "4"];
const MEMORY_OPTIONS = ["256Mi", "512Mi", "1Gi", "2Gi", "4Gi", "8Gi"];

const RESOURCE_TYPES = [
	{
		value: "SERVICE",
		label: "Service",
		description: "Deploy web services, APIs, and backend applications",
		icon: Server,
		available: true,
	},
	{
		value: "DATABASE",
		label: "Database",
		description: "Managed PostgreSQL, MySQL, and more",
		icon: Database,
		available: false,
	},
	{
		value: "FUNCTION",
		label: "Function",
		description: "Event-driven serverless functions",
		icon: Zap,
		available: false,
	},
	{
		value: "CACHE",
		label: "Cache",
		description: "In-memory data caching with Redis",
		icon: Layers,
		available: false,
	},
	{
		value: "QUEUE",
		label: "Queue",
		description: "Asynchronous message queues",
		icon: Mail,
		available: false,
	},
	{
		value: "BLOB",
		label: "Blob Storage",
		description: "Object storage for files and media",
		icon: HardDrive,
		available: false,
	},
];

export function CreateResource() {
	const navigate = useNavigate();
	const { workspaceId: paramWorkspaceId } = useParams();
	const [searchParams] = useSearchParams();
	const workspaceFromUrl = searchParams.get("workspace");

	const [resourceName, setResourceName] = useState("");
	const [resourceType, setResourceType] = useState("SERVICE");
	const [subdomain, setSubdomain] = useState("");
	const [selectedPlatformDomain, setSelectedPlatformDomain] =
		useState<string>("");
	const [subdomainAvailability, setSubdomainAvailability] = useState<
		"available" | "unavailable" | "checking" | null
	>(null);
	const hasUserEditedSubdomain = useRef(false);
	const checkSubdomainTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(
		null
	);

	// Docker deployment fields
	const [dockerImageUrl, setDockerImageUrl] = useState("");
	const [appPort, setAppPort] = useState("");
	const [dockerImageError, setDockerImageError] = useState<string>("");

	// Deployment configuration (all optional with defaults)
	const [region, setRegion] = useState("us-east-1");
	const [cpuIndex, setCpuIndex] = useState(1); // 0.5 vCPU
	const [memoryIndex, setMemoryIndex] = useState(1); // 512Mi
	const [envVars, setEnvVars] = useState<{ key: string; value: string }[]>([]);
	const [isEnvModalOpen, setIsEnvModalOpen] = useState(false);
	const [envFileContent, setEnvFileContent] = useState("");

	const addEnvVar = () => {
		setEnvVars([...envVars, { key: "", value: "" }]);
	};

	const removeEnvVar = (index: number) => {
		setEnvVars(envVars.filter((_, i) => i !== index));
	};

	const updateEnvVar = (
		index: number,
		field: "key" | "value",
		value: string
	) => {
		const updated = [...envVars];
		updated[index][field] = value;
		setEnvVars(updated);
	};

	const parseEnvFile = (content: string) => {
		const lines = content.split("\n");
		const parsed: { key: string; value: string }[] = [];

		for (const line of lines) {
			const trimmed = line.trim();
			// Skip empty lines and comments
			if (!trimmed || trimmed.startsWith("#")) continue;

			// Parse KEY=VALUE format
			const equalIndex = trimmed.indexOf("=");
			if (equalIndex > 0) {
				const key = trimmed.substring(0, equalIndex).trim();
				let value = trimmed.substring(equalIndex + 1).trim();

				// Remove quotes if present
				if (
					(value.startsWith('"') && value.endsWith('"')) ||
					(value.startsWith("'") && value.endsWith("'"))
				) {
					value = value.slice(1, -1);
				}

				if (key) {
					parsed.push({ key, value });
				}
			}
		}

		return parsed;
	};

	const handleImportEnvFile = () => {
		const parsed = parseEnvFile(envFileContent);
		setEnvVars(parsed);
		setIsEnvModalOpen(false);
		setEnvFileContent("");
		toast.success(
			`Imported ${parsed.length} environment variable${
				parsed.length !== 1 ? "s" : ""
			}`
		);
	};

	const { user } = useAuth();

	const { data: orgsRes } = useQuery(
		listUserOrgs,
		{ userId: user?.id ?? 0n },
		{ enabled: !!user }
	);
	const orgs = orgsRes?.orgs ?? [];
	const firstOrgId = orgs.length > 0 ? orgs[0].id : null;

	const { data: workspacesRes } = useQuery(
		listOrgWorkspaces,
		firstOrgId ? { orgId: firstOrgId } : undefined,
		{ enabled: !!firstOrgId }
	);
	const workspaces = workspacesRes?.workspaces ?? [];
	const workspaceId =
		paramWorkspaceId || (workspaces.length > 0 ? workspaces[0].id : null);

	const { data: platformDomainsRes } = useQuery(listPlatformDomains, {
		activeOnly: true,
	});
	const platformDomains = useMemo(
		() => platformDomainsRes?.platformDomains ?? [],
		[platformDomainsRes?.platformDomains]
	);

	const createResourceMutation = useMutation(createResource);
	const createDeploymentMutation = useMutation(createDeployment);
	const checkSubdomainMutation = useMutation(checkDomainAvailability);

	// Set default platform domain on load
	useEffect(() => {
		if (platformDomains.length > 0 && !selectedPlatformDomain) {
			setSelectedPlatformDomain(platformDomains[0].domain);
		}
	}, [platformDomains, selectedPlatformDomain]);

	// Auto-fill subdomain from resource name (only if user hasn't manually edited it)
	useEffect(() => {
		if (hasUserEditedSubdomain.current) return;

		const sanitized = resourceName
			.toLowerCase()
			.replace(/[^a-z0-9-]/g, "")
			.replace(/^-+|-+$/g, "");
		setSubdomain(sanitized);
	}, [resourceName]);

	// Check subdomain availability as user types (debounced)
	useEffect(() => {
		// Clear previous timeout
		if (checkSubdomainTimeoutRef.current) {
			clearTimeout(checkSubdomainTimeoutRef.current);
		}

		if (!subdomain.trim()) {
			setSubdomainAvailability(null);
			return;
		}

		setSubdomainAvailability("checking");

		checkSubdomainTimeoutRef.current = setTimeout(async () => {
			try {
				const fullDomain = `${subdomain.trim()}.${selectedPlatformDomain}`;
				const availabilityRes = await checkSubdomainMutation.mutateAsync({
					domain: fullDomain,
				});

				setSubdomainAvailability(
					availabilityRes.isAvailable ? "available" : "unavailable"
				);
			} catch (error) {
				toastConnectError(error);
				setSubdomainAvailability(null);
			}
		}, 500); // 500ms debounce

		return () => {
			if (checkSubdomainTimeoutRef.current) {
				clearTimeout(checkSubdomainTimeoutRef.current);
			}
		};
	}, [subdomain, selectedPlatformDomain, platformDomains]);

	// Validate Docker image URL
	const validateDockerImage = (image: string): string => {
		if (!image.trim()) return "";

		// Basic validation rules:
		// 1. No spaces
		if (/\s/.test(image)) {
			return "Image URL cannot contain spaces";
		}

		// 2. Check for valid characters (alphanumeric, dots, hyphens, slashes, colons, underscores)
		if (!/^[a-zA-Z0-9.\-/:_@]+$/.test(image)) {
			return "Image URL contains invalid characters";
		}

		// 3. Should not start or end with special characters
		if (/^[./:@-]|[./:@-]$/.test(image)) {
			return "Image URL cannot start or end with special characters";
		}

		// 4. If contains registry, validate basic format (registry.com/image:tag)
		if (image.includes("/")) {
			const parts = image.split("/");
			// Check if any part is empty
			if (parts.some((p) => p.length === 0)) {
				return "Invalid image path format";
			}
		}

		// 5. If contains tag (after :), validate it's not empty
		if (image.includes(":")) {
			const colonIndex = image.lastIndexOf(":");
			const tag = image.substring(colonIndex + 1);
			if (tag.length === 0) {
				return "Tag cannot be empty";
			}
			// Tag should not contain slashes (those belong before the colon)
			if (tag.includes("/")) {
				return "Invalid tag format";
			}
		}

		return "";
	};

	const handleSubmit = async (e: React.FormEvent) => {
		e.preventDefault();

		if (!resourceName.trim()) {
			toast.error("Resource name is required");
			return;
		}

		if (!workspaceId) {
			toast.error("No workspace available");
			return;
		}

		// Validate Docker image fields if provided
		if (dockerImageUrl.trim()) {
			const imageError = validateDockerImage(dockerImageUrl);
			if (imageError) {
				toast.error(imageError);
				return;
			}

			if (!appPort.trim()) {
				toast.error("App port is required when deploying a Docker image");
				return;
			}
		}

		// Block if subdomain is unavailable (checked in real-time as user types)
		if (subdomain.trim() && subdomainAvailability === "unavailable") {
			toast.error("The chosen subdomain is not available");
			return;
		}

		try {
			const platformDomain = platformDomains.find(
				(d) => d.domain === selectedPlatformDomain
			);

			// Build spec based on resource type
			const routing = create(RoutingConfigSchema, {
				port: parseInt(appPort || "3000", 10) || 3000,
				pathPrefix: "/",
				idleTimeout: 30,
			});

			const logging = create(LoggingConfigSchema, {
				enabled: true,
				retentionPeriod: "7d",
				structured: true,
			});

			const metrics = create(MetricsConfigSchema, {
				enabled: true,
				path: "/metrics",
				port: 9090,
			});

			const tracing = create(TracingConfigSchema, {
				enabled: false,
				sampleRate: 0.1,
				tags: {},
			});

			const regionTarget = create(RegionTargetSchema, {
				enabled: true,
				primary: true,
				cpu: CPU_OPTIONS[cpuIndex],
				memory: MEMORY_OPTIONS[memoryIndex],
				minReplicas: 1,
				maxReplicas: 1,
			});

			const serviceSpec = create(ServiceSpecSchema, {
				routing,
				observability: {
					logging,
					metrics,
					tracing,
				},
				regions: {
					[region]: regionTarget,
				},
			});

			const spec = create(ResourceSpecSchema, {
				spec: {
					case: "service",
					value: serviceSpec,
				},
			});

			// Create the resource
			const resource = await createResourceMutation.mutateAsync({
				name: resourceName,
				workspaceId:
					typeof workspaceId === "string" ? BigInt(workspaceId) : workspaceId,
				type: ResourceType[resourceType as keyof typeof ResourceType],
				domain: {
					domainSource: DomainType.PLATFORM_PROVIDED,
					subdomain: subdomain,
					platformDomainId: platformDomain?.id || BigInt(0),
				},
				spec,
			});

			if (!resource?.resourceId) {
				toast.error("Failed to create resource");
				return;
			}

			// If Docker image is provided, create a deployment
			if (dockerImageUrl.trim() && appPort.trim()) {
				try {
					// Convert env vars array to object
					const envObject: { [key: string]: string } = {};
					envVars.forEach((env) => {
						if (env.key.trim() && env.value.trim()) {
							envObject[env.key.trim()] = env.value.trim();
						}
					});

					await createDeploymentMutation.mutateAsync({
						resourceId: resource.resourceId,
						clusterId: BigInt(1), // Default cluster
						region: region,
						spec: {
							spec: {
								case: "service",
								value: {
									build: {
										type: "image",
										image: dockerImageUrl.trim(),
									},
									cpu: CPU_OPTIONS[cpuIndex],
									memory: MEMORY_OPTIONS[memoryIndex],
									minReplicas: 1,
									maxReplicas: 1,
									port: parseInt(appPort, 10),
									env: envObject,
								},
							},
						},
					});
					toast.success("Resource and deployment created successfully");
				} catch (deployError) {
					// Resource was created but deployment failed
					toast.warning(
						`Resource created, but deployment failed: ${getErrorMessage(
							deployError,
							"Unknown error"
						)}`
					);
				}
			} else {
				toast.success("Resource created successfully");
			}

			// Navigate to resource details
			navigate(
				`/resource/${resource.resourceId}${
					workspaceFromUrl ? `?workspace=${workspaceFromUrl}` : ""
				}`
			);
		} catch (error) {
			toast.error(getErrorMessage(error, "Failed to create resource"));
		}
	};

	const selectedResourceType = RESOURCE_TYPES.find(
		(t) => t.value === resourceType
	);
	const isCreating =
		createResourceMutation.isPending || createDeploymentMutation.isPending;

	return (
		<div className="max-w-4xl mx-auto">
			<div className="mb-8">
				<h1 className="text-3xl font-heading text-foreground mb-2">
					Create New Resource
				</h1>
			</div>

			<form onSubmit={handleSubmit} className="space-y-6">
				{/* Resource Type Selection - Featured Section */}
				<Card className="border-2">
					<CardHeader>
						<CardTitle className="text-xl">Choose Resource Type</CardTitle>
						<CardDescription>
							Select the type of resource you want to deploy
						</CardDescription>
					</CardHeader>
					<CardContent>
						<div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
							{RESOURCE_TYPES.map((type) => {
								const Icon = type.icon;
								const isSelected = resourceType === type.value;
								return (
									<button
										key={type.value}
										type="button"
										onClick={() =>
											type.available && setResourceType(type.value)
										}
										disabled={!type.available}
										className={`relative p-5 rounded-xl text-left transition-all duration-200 ${
											isSelected
												? "bg-main/10 shadow-lg scale-[1.02]"
												: type.available
												? "hover:shadow-md hover:scale-[1.01]"
												: "opacity-60 cursor-not-allowed"
										}`}
									>
										<div className="flex items-start gap-3 mb-3">
											<div
												className={`p-2 rounded-lg ${
													isSelected ? "bg-main text-white" : "bg-secondary"
												}`}
											>
												<Icon className="h-5 w-5" />
											</div>
											<div className="flex-1">
												<div className="font-semibold text-foreground">
													{type.label}
												</div>
											</div>
										</div>
										<div className="text-xs text-muted-foreground">
											{type.description}
										</div>
									</button>
								);
							})}
						</div>
					</CardContent>
				</Card>

				{/* Resource Configuration */}
				<Card>
					<CardHeader>
						<CardTitle className="text-lg">Resource Configuration</CardTitle>
						<CardDescription>Basic settings for your resource</CardDescription>
					</CardHeader>
					<CardContent className="space-y-4">
						{/* Resource Name */}
						<div className="space-y-2">
							<Label htmlFor="resource-name" className="text-sm font-medium">
								Resource Name
							</Label>
							<Input
								id="resource-name"
								placeholder="my-awesome-app"
								value={resourceName}
								onChange={(e) => setResourceName(e.target.value)}
								className="border-border"
							/>
							<p className="text-xs text-muted-foreground">
								Choose a descriptive name for your resource
							</p>
						</div>

						{/* Domain Configuration */}
						<div className="space-y-4 pt-4 border-t">
							<div>
								<Label className="text-sm font-medium">Domain</Label>
								<p className="text-xs text-muted-foreground mt-1">
									Your resource will be accessible at this URL
								</p>
							</div>

							<div className="space-y-2">
								<Label htmlFor="platform-domain" className="text-sm">
									Platform Domain
								</Label>
								<Select
									value={selectedPlatformDomain}
									onValueChange={setSelectedPlatformDomain}
								>
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
									<div className="flex items-center px-3 bg-secondary rounded-lg border border-border text-sm text-muted-foreground shrink-0 min-w-fit">
										.{selectedPlatformDomain}
									</div>
									{subdomainAvailability && (
										<div className="flex items-center px-3">
											{subdomainAvailability === "checking" && (
												<Loader className="h-4 w-4 animate-spin text-foreground" />
											)}
											{subdomainAvailability === "available" && (
												<Check className="h-4 w-4 text-green-600" />
											)}
											{subdomainAvailability === "unavailable" && (
												<X className="h-4 w-4 text-destructive" />
											)}
										</div>
									)}
								</div>
								{subdomainAvailability === "unavailable" && (
									<p className="text-xs text-destructive">
										This domain is not available.
									</p>
								)}
								{subdomain &&
									selectedPlatformDomain &&
									subdomainAvailability !== "unavailable" && (
										<p className="text-xs text-muted-foreground">
											Full URL: https://{subdomain}.{selectedPlatformDomain}
										</p>
									)}
							</div>
						</div>
					</CardContent>
				</Card>

				{/* Deployment Configuration - Only show for SERVICE type */}
				{selectedResourceType?.available && resourceType === "SERVICE" && (
					<Card className="border-dashed border-2 border-main/30 bg-main/5">
						<CardHeader>
							<CardTitle className="text-lg flex items-center gap-2">
								<Server className="h-5 w-5 text-main" />
								Deploy Now (Optional)
							</CardTitle>
							<CardDescription>
								Provide a Docker image to create an initial deployment
							</CardDescription>
						</CardHeader>
						<CardContent className="space-y-4">
							<div className="space-y-2">
								<Label htmlFor="docker-image" className="text-sm font-medium">
									Docker Image URL
								</Label>
								<Input
									id="docker-image"
									placeholder="nginx:latest or registry.example.com/my-app:v1.0.0"
									value={dockerImageUrl}
									onChange={(e) => {
										const value = e.target.value;
										setDockerImageUrl(value);
										if (value.trim()) {
											setDockerImageError(validateDockerImage(value));
										} else {
											setDockerImageError("");
										}
									}}
									className={`border-border bg-background ${
										dockerImageError ? "border-error-text" : ""
									}`}
								/>
								{dockerImageError ? (
									<p className="text-xs text-error-text">{dockerImageError}</p>
								) : (
									<p className="text-xs text-muted-foreground">
										Leave empty to configure deployment later
									</p>
								)}
							</div>

							{dockerImageUrl.trim() && (
								<div className="space-y-4 animate-in slide-in-from-top-2 duration-300">
									{/* App Port */}
									<div className="space-y-2">
										<Label htmlFor="app-port" className="text-sm font-medium">
											Application Port{" "}
											<span className="text-error-text">*</span>
										</Label>
										<Input
											id="app-port"
											type="number"
											placeholder="8080"
											value={appPort}
											onChange={(e) => setAppPort(e.target.value)}
											className="border-border bg-background w-32"
											min="1"
											max="65535"
										/>
										<p className="text-xs text-muted-foreground">
											The port your application listens on inside the container
										</p>
									</div>

									{/* Region Selection */}
									<div className="space-y-2">
										<Label htmlFor="region" className="text-sm font-medium">
											Region
										</Label>
										<Select value={region} onValueChange={setRegion}>
											<SelectTrigger
												id="region"
												className="border-border bg-background"
											>
												<SelectValue />
											</SelectTrigger>
											<SelectContent>
												{REGIONS.map((r) => (
													<SelectItem key={r.value} value={r.value}>
														{r.label}
													</SelectItem>
												))}
											</SelectContent>
										</Select>
										<p className="text-xs text-muted-foreground">
											Choose the region where your service will be deployed
										</p>
									</div>

									{/* Resource Configuration */}
									<div className="space-y-4 pt-3 border-t border-border">
										<Label className="text-sm font-medium">
											Resource Limits (Optional)
										</Label>
										<div className="grid grid-cols-2 gap-6">
											{/* CPU Slider */}
											<div className="space-y-3">
												<div className="flex items-center justify-between">
													<Label className="text-xs text-muted-foreground">
														CPU
													</Label>
													<span className="text-sm font-semibold text-foreground">
														{CPU_OPTIONS[cpuIndex]} vCPU
													</span>
												</div>
												<Slider
													value={[cpuIndex]}
													onValueChange={(value) => setCpuIndex(value[0])}
													min={0}
													max={CPU_OPTIONS.length - 1}
													step={1}
													className="w-full"
												/>
												<div className="flex justify-between text-xs text-muted-foreground">
													<span>{CPU_OPTIONS[0]}</span>
													<span>{CPU_OPTIONS[CPU_OPTIONS.length - 1]}</span>
												</div>
											</div>

											{/* Memory Slider */}
											<div className="space-y-3">
												<div className="flex items-center justify-between">
													<Label className="text-xs text-muted-foreground">
														Memory
													</Label>
													<span className="text-sm font-semibold text-foreground">
														{MEMORY_OPTIONS[memoryIndex]}
													</span>
												</div>
												<Slider
													value={[memoryIndex]}
													onValueChange={(value) => setMemoryIndex(value[0])}
													min={0}
													max={MEMORY_OPTIONS.length - 1}
													step={1}
													className="w-full"
												/>
												<div className="flex justify-between text-xs text-muted-foreground">
													<span>{MEMORY_OPTIONS[0]}</span>
													<span>
														{MEMORY_OPTIONS[MEMORY_OPTIONS.length - 1]}
													</span>
												</div>
											</div>
										</div>
									</div>

									{/* Environment Variables */}
									<div className="space-y-3 pt-3 border-t border-border">
										<div className="flex items-center justify-between">
											<Label className="text-sm font-medium">
												Environment Variables (Optional)
											</Label>
											<div className="flex gap-2">
												<Button
													type="button"
													variant="outline"
													size="sm"
													onClick={() => setIsEnvModalOpen(true)}
													className="h-8"
												>
													<FileText className="h-4 w-4 mr-1" />
													Import .env
												</Button>
												<Button
													type="button"
													variant="outline"
													size="sm"
													onClick={addEnvVar}
													className="h-8"
												>
													<Plus className="h-4 w-4 mr-1" />
													Add Variable
												</Button>
											</div>
										</div>
										{envVars.length === 0 ? (
											<p className="text-xs text-muted-foreground">
												No environment variables added yet
											</p>
										) : (
											<div className="space-y-2">
												{envVars.map((env, index) => (
													<div key={index} className="flex gap-2">
														<Input
															placeholder="KEY"
															value={env.key}
															onChange={(e) =>
																updateEnvVar(index, "key", e.target.value)
															}
															className="border-border bg-background flex-1 font-mono text-sm"
														/>
														<Input
															placeholder="value"
															value={env.value}
															onChange={(e) =>
																updateEnvVar(index, "value", e.target.value)
															}
															className="border-border bg-background flex-1"
														/>
														<Button
															type="button"
															variant="ghost"
															size="sm"
															onClick={() => removeEnvVar(index)}
															className="h-10 px-3 text-muted-foreground hover:text-error-text"
														>
															<Trash2 className="h-4 w-4" />
														</Button>
													</div>
												))}
											</div>
										)}
									</div>
								</div>
							)}
						</CardContent>
					</Card>
				)}

				{/* Actions */}
				<div className="flex gap-3 justify-end pt-4">
					<Button
						type="button"
						variant="secondary"
						onClick={() => navigate("/dashboard")}
						disabled={isCreating}
					>
						Cancel
					</Button>
					<Button
						type="submit"
						disabled={
							isCreating ||
							!resourceName.trim() ||
							!selectedResourceType?.available ||
							(subdomain.trim() !== "" &&
								(subdomainAvailability === "unavailable" ||
									subdomainAvailability === "checking")) ||
							!!dockerImageError
						}
						className="min-w-[140px]"
					>
						{isCreating ? (
							<>
								<Loader className="h-4 w-4 animate-spin mr-2" />
								Creating...
							</>
						) : dockerImageUrl.trim() ? (
							"Create & Deploy"
						) : (
							"Create Resource"
						)}
					</Button>
				</div>
			</form>

			{/* .env Import Modal */}
			<Dialog open={isEnvModalOpen} onOpenChange={setIsEnvModalOpen}>
				<DialogContent className="max-w-3xl max-h-[80vh]">
					<DialogHeader>
						<DialogTitle>Import Environment Variables</DialogTitle>
						<DialogDescription>
							Paste your .env file contents below. We'll parse it and add all
							variables.
						</DialogDescription>
					</DialogHeader>
					<div className="space-y-4">
						<Textarea
							value={envFileContent}
							onChange={(e) => setEnvFileContent(e.target.value)}
							placeholder="DATABASE_URL=postgresql://user:pass@localhost:5432/db&#10;API_KEY=your-api-key-here&#10;NODE_ENV=production&#10;# Comments are supported"
							className="font-mono text-sm min-h-[400px] resize-none border-border"
							spellCheck={false}
						/>
						<p className="text-xs text-muted-foreground">
							Supports KEY=VALUE format. Comments (lines starting with #) and
							empty lines will be ignored.
						</p>
					</div>
					<DialogFooter>
						<Button
							type="button"
							variant="secondary"
							onClick={() => {
								setIsEnvModalOpen(false);
								setEnvFileContent("");
							}}
						>
							Cancel
						</Button>
						<Button
							type="button"
							onClick={handleImportEnvFile}
							disabled={!envFileContent.trim()}
						>
							Import Variables
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</div>
	);
}
