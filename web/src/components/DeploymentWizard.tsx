import { Button } from "@/components/design/Button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/design/Dialog";
import { Input } from "@/components/design/Input";
import { Label } from "@/components/design/Label";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/design/Select";
import { Slider } from "@/components/ui/slider";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { checkDomainAvailability } from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import { cn } from "@/lib/utils";
import { useMutation } from "@connectrpc/connect-query";
import {
	Check,
	Eye,
	EyeOff,
	FileText,
	Loader,
	Plus,
	Trash2,
	X,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";

const REGIONS = [
	{ value: "us-east-1", label: "US East (N. Virginia)" },
	{ value: "us-west-2", label: "US West (Oregon)" },
	{ value: "eu-west-1", label: "EU West (Ireland)" },
	{ value: "ap-southeast-1", label: "Asia Pacific (Singapore)" },
];

const CPU_OPTIONS = ["0.25", "0.5", "1", "2", "4"];
const MEMORY_OPTIONS = ["256Mi", "512Mi", "1Gi", "2Gi", "4Gi", "8Gi"];

const STEPS = [
	{ id: 1, label: "From" },
	{ id: 2, label: "Network" },
	{ id: 3, label: "Resources" },
	{ id: 4, label: "Environment" },
] as const;

function validateDockerImage(image: string): string {
	if (!image.trim()) return "";
	if (/\s/.test(image)) return "Image URL cannot contain spaces";
	if (!/^[a-zA-Z0-9.\-/:_@]+$/.test(image))
		return "Image URL contains invalid characters";
	if (/^[./:@-]|[./:@-]$/.test(image))
		return "Image URL cannot start or end with special characters";
	if (image.includes("/")) {
		const parts = image.split("/");
		if (parts.some((p) => p.length === 0)) return "Invalid image path format";
	}
	if (image.includes(":")) {
		const colonIndex = image.lastIndexOf(":");
		const tag = image.substring(colonIndex + 1);
		if (tag.length === 0) return "Tag cannot be empty";
		if (tag.includes("/")) return "Invalid tag format";
	}
	return "";
}

function parseEnvFile(content: string): { key: string; value: string }[] {
	const parsed: { key: string; value: string }[] = [];
	for (const line of content.split("\n")) {
		const trimmed = line.trim();
		if (!trimmed || trimmed.startsWith("#")) continue;
		const equalIndex = trimmed.indexOf("=");
		if (equalIndex > 0) {
			const key = trimmed.substring(0, equalIndex).trim();
			let value = trimmed.substring(equalIndex + 1).trim();
			if (
				(value.startsWith('"') && value.endsWith('"')) ||
				(value.startsWith("'") && value.endsWith("'"))
			) {
				value = value.slice(1, -1);
			}
			if (key) parsed.push({ key, value });
		}
	}
	return parsed;
}

export interface DeploymentWizardValues {
	imageUrl: string;
	networkEnabled: boolean;
	port: number;
	subdomain: string;
	region: string;
	cpu: string;
	memory: string;
	replicas: number;
	envVars: Record<string, string>;
}

export interface DeploymentWizardProps {
	open: boolean;
	onClose: () => void;
	title?: string;
	submitLabel?: string;
	/** Show subdomain field in step 2 (only needed when creating a new resource) */
	showSubdomain?: boolean;
	platformDomain?: { id: string; domain: string };
	initialSubdomain?: string;
	onSubmit: (values: DeploymentWizardValues) => Promise<void>;
	isSubmitting: boolean;
}

export function DeploymentWizard({
	open,
	onClose,
	title = "Deploy",
	submitLabel = "Deploy",
	showSubdomain = false,
	platformDomain,
	initialSubdomain = "",
	onSubmit,
	isSubmitting,
}: DeploymentWizardProps) {
	const [step, setStep] = useState(1);

	// Step 1: From
	const [imageUrl, setImageUrl] = useState("");
	const [imageError, setImageError] = useState("");

	// Step 2: Network
	const [networkEnabled, setNetworkEnabled] = useState(true);
	const [subdomain, setSubdomain] = useState(initialSubdomain);
	const [port, setPort] = useState("3000");
	const [subdomainAvailability, setSubdomainAvailability] = useState<
		"available" | "unavailable" | "checking" | null
	>(null);
	const checkTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
	const checkSubdomainMut = useMutation(checkDomainAvailability);
	const checkMutateRef = useRef(checkSubdomainMut.mutateAsync);
	useEffect(() => {
		checkMutateRef.current = checkSubdomainMut.mutateAsync;
	});

	// Step 3: Resources
	const [region, setRegion] = useState("us-east-1");
	const [cpuIndex, setCpuIndex] = useState(1); // default "0.5"
	const [memIndex, setMemIndex] = useState(1); // default "512Mi"

	// Step 4: Environment
	const [envVars, setEnvVars] = useState<
		{ id: string; key: string; value: string }[]
	>([]);
	const [revealedVars, setRevealedVars] = useState<Set<number>>(
		() => new Set(),
	);
	const [revealCountdowns, setRevealCountdowns] = useState<Map<number, number>>(
		() => new Map(),
	);
	const revealTimers = useRef<Map<number, ReturnType<typeof setTimeout>>>(
		new Map(),
	);
	const revealIntervals = useRef<Map<number, ReturnType<typeof setInterval>>>(
		new Map(),
	);
	const [isEnvModalOpen, setIsEnvModalOpen] = useState(false);
	const [envFileContent, setEnvFileContent] = useState("");
	const contentScrollRef = useRef<HTMLDivElement>(null);

	// Reset all state when dialog opens
	useEffect(() => {
		if (!open) return;
		setStep(1);
		setImageUrl("");
		setImageError("");
		setNetworkEnabled(true);
		setSubdomain(initialSubdomain);
		setPort("3000");
		setSubdomainAvailability(null);
		setRegion("us-east-1");
		setCpuIndex(1);
		setMemIndex(1);
		setEnvVars([]);
		setRevealedVars(new Set());
		setRevealCountdowns(new Map());
	}, [open, initialSubdomain]);

	// Subdomain availability check (debounced, only when showSubdomain=true)
	useEffect(() => {
		if (!showSubdomain) return;
		if (checkTimeoutRef.current) clearTimeout(checkTimeoutRef.current);

		if (!subdomain.trim()) {
			const t = setTimeout(() => {
				setSubdomainAvailability(null);
			}, 0);
			return () => {
				clearTimeout(t);
			};
		}
		const domainName = platformDomain?.domain;
		if (!domainName) {
			const t = setTimeout(() => {
				setSubdomainAvailability(null);
			}, 0);
			return () => {
				clearTimeout(t);
			};
		}

		const checkingTimer = setTimeout(() => {
			setSubdomainAvailability("checking");
		}, 0);
		checkTimeoutRef.current = setTimeout(async () => {
			try {
				const res = await checkMutateRef.current({
					domain: `${subdomain.trim()}.${domainName}`,
				});
				setSubdomainAvailability(res.isAvailable ? "available" : "unavailable");
			} catch {
				setSubdomainAvailability(null);
			}
		}, 500);

		return () => {
			clearTimeout(checkingTimer);
			if (checkTimeoutRef.current) clearTimeout(checkTimeoutRef.current);
		};
	}, [subdomain, platformDomain, showSubdomain]);

	// Cleanup reveal timers on unmount
	useEffect(() => {
		const timers = revealTimers.current;
		const intervals = revealIntervals.current;
		return () => {
			timers.forEach(clearTimeout);
			intervals.forEach(clearInterval);
		};
	}, []);

	const revealVar = (index: number) => {
		const t = revealTimers.current.get(index);
		if (t) clearTimeout(t);
		const iv = revealIntervals.current.get(index);
		if (iv) clearInterval(iv);

		setRevealedVars((prev) => new Set([...prev, index]));
		setRevealCountdowns((prev) => new Map([...prev, [index, 10]]));

		const interval = setInterval(() => {
			setRevealCountdowns((prev) => {
				const next = new Map(prev);
				const current = next.get(index) ?? 0;
				if (current <= 1) next.delete(index);
				else next.set(index, current - 1);
				return next;
			});
		}, 1000);
		revealIntervals.current.set(index, interval);

		const timer = setTimeout(() => {
			setRevealedVars((prev) => {
				const n = new Set(prev);
				n.delete(index);
				return n;
			});
			clearInterval(interval);
			revealTimers.current.delete(index);
			revealIntervals.current.delete(index);
		}, 10000);
		revealTimers.current.set(index, timer);
	};

	const hideVar = (index: number) => {
		const t = revealTimers.current.get(index);
		const iv = revealIntervals.current.get(index);
		if (t) clearTimeout(t);
		if (iv) clearInterval(iv);
		revealTimers.current.delete(index);
		revealIntervals.current.delete(index);
		setRevealedVars((prev) => {
			const n = new Set(prev);
			n.delete(index);
			return n;
		});
		setRevealCountdowns((prev) => {
			const n = new Map(prev);
			n.delete(index);
			return n;
		});
	};

	const addEnvVar = () => {
		setEnvVars([...envVars, { id: crypto.randomUUID(), key: "", value: "" }]);
		setTimeout(() => {
			if (contentScrollRef.current)
				contentScrollRef.current.scrollTop =
					contentScrollRef.current.scrollHeight;
		}, 30);
	};

	const removeEnvVar = (index: number) => {
		revealTimers.current.forEach(clearTimeout);
		revealIntervals.current.forEach(clearInterval);
		revealTimers.current.clear();
		revealIntervals.current.clear();
		setRevealedVars(new Set());
		setRevealCountdowns(new Map());
		setEnvVars(envVars.filter((_, i) => i !== index));
	};

	const updateEnvVar = (
		index: number,
		field: "key" | "value",
		value: string,
	) => {
		const updated = [...envVars];
		updated[index][field] = value;
		setEnvVars(updated);
	};

	const handleImportEnvFile = () => {
		const parsed = parseEnvFile(envFileContent).map((v) => ({
			...v,
			id: crypto.randomUUID(),
		}));
		setEnvVars(parsed);
		setIsEnvModalOpen(false);
		setEnvFileContent("");
		toast.success(
			`Imported ${String(parsed.length)} environment variable${parsed.length !== 1 ? "s" : ""}`,
		);
	};

	const canProceedStep1 = imageUrl.trim() !== "" && !imageError;
	const canProceedStep2 =
		!networkEnabled ||
		(port.trim() !== "" &&
			(!showSubdomain ||
				(subdomainAvailability !== "unavailable" &&
					subdomainAvailability !== "checking")));

	const handleNext = () => {
		if (step === 1) {
			const err = validateDockerImage(imageUrl);
			if (err) {
				setImageError(err);
				return;
			}
		}
		if (step < 4) {
			setStep(step + 1);
			return;
		}
		void handleSubmit();
	};

	const handleSubmit = async () => {
		const envObject: Record<string, string> = {};
		envVars.forEach((env) => {
			if (env.key.trim()) envObject[env.key.trim()] = env.value.trim();
		});
		await onSubmit({
			imageUrl: imageUrl.trim(),
			networkEnabled,
			port: parseInt(port || "3000", 10) || 3000,
			subdomain: subdomain.trim(),
			region,
			cpu: CPU_OPTIONS[cpuIndex],
			memory: MEMORY_OPTIONS[memIndex],
			replicas: 1,
			envVars: envObject,
		});
	};

	return (
		<>
			<Dialog
				open={open}
				onOpenChange={(isOpen) => {
					if (!isOpen && !isSubmitting) onClose();
				}}
			>
				<DialogContent className="max-w-xl p-0 gap-0 overflow-hidden">
					{/* Header */}
					<div className="px-6 pt-6 pb-5 border-b border-border">
						<DialogHeader className="mb-5">
							<DialogTitle className="text-base font-semibold">
								{title}
							</DialogTitle>
						</DialogHeader>

						{/* Step indicator */}
						<div className="flex items-start">
							{STEPS.map((s, idx) => (
								<div
									key={s.id}
									className={cn(
										"flex items-start",
										idx < STEPS.length - 1 ? "flex-1" : "",
									)}
								>
									<div className="flex flex-col items-center gap-1.5">
										<button
											type="button"
											onClick={() => {
												if (step > s.id) setStep(s.id);
											}}
											disabled={step <= s.id}
											className={cn(
												"w-7 h-7 rounded-full flex items-center justify-center text-xs font-semibold transition-all duration-200 shrink-0",
												step > s.id
													? "bg-primary text-primary-foreground cursor-pointer hover:bg-primary/85 shadow-sm"
													: step === s.id
														? "bg-primary text-primary-foreground ring-2 ring-primary/30 ring-offset-2 ring-offset-background"
														: "bg-muted text-muted-foreground border border-border",
											)}
										>
											{step > s.id ? <Check className="h-3.5 w-3.5" /> : s.id}
										</button>
										<span
											className={cn(
												"text-xs whitespace-nowrap transition-colors duration-200",
												step === s.id
													? "text-foreground font-medium"
													: step > s.id
														? "text-muted-foreground"
														: "text-muted-foreground/60",
											)}
										>
											{s.label}
										</span>
									</div>
									{idx < STEPS.length - 1 && (
										<div
											className={cn(
												"flex-1 h-px mx-2 mt-3.5 transition-colors duration-300",
												step > s.id ? "bg-primary/50" : "bg-border",
											)}
										/>
									)}
								</div>
							))}
						</div>
					</div>

					{/* Step content */}
					<div
						ref={contentScrollRef}
						className="px-6 py-5 min-h-[300px] max-h-[420px] overflow-y-auto"
					>
						{/* Step 1: From */}
						{step === 1 && (
							<div className="space-y-4">
								<div>
									<p className="text-sm font-medium text-foreground">
										Docker Image
									</p>
									<p className="text-xs text-muted-foreground mt-0.5">
										Enter the Docker image URL for your application
									</p>
								</div>
								<div className="space-y-1.5">
									<Label htmlFor="dw-docker-image" className="text-sm">
										Image URL
									</Label>
									<Input
										id="dw-docker-image"
										placeholder="nginx:latest or registry.example.com/my-app:v1.0.0"
										value={imageUrl}
										onChange={(e) => {
											const val = e.target.value;
											setImageUrl(val);
											setImageError(val.trim() ? validateDockerImage(val) : "");
										}}
										className={cn(imageError && "border-destructive")}
										autoFocus
									/>
									{imageError ? (
										<p className="text-xs text-destructive">{imageError}</p>
									) : (
										<p className="text-xs text-muted-foreground">
											e.g.{" "}
											<code className="font-mono bg-muted px-1 py-0.5 rounded text-xs">
												nginx:latest
											</code>{" "}
											or{" "}
											<code className="font-mono bg-muted px-1 py-0.5 rounded text-xs">
												ghcr.io/org/app:v1.2.3
											</code>
										</p>
									)}
								</div>
							</div>
						)}

						{/* Step 2: Network */}
						{step === 2 && (
							<div className="space-y-4">
								<div
									className={cn(
										"flex items-center justify-between px-3 py-3 rounded-lg border border-border w-full gap-6",
										!networkEnabled && "bg-amber-500/10 border-amber-500/20",
									)}
								>
									<div>
										<p className="text-sm font-medium">
											Enable internet access
										</p>
										<p className="text-xs text-muted-foreground mt-0.5">
											{networkEnabled ? (
												<span>Your app will receive internet traffic</span>
											) : (
												<span className="text-destructive">
													Your app will not receive internet traffic.
												</span>
											)}
										</p>
									</div>
									<Switch
										checked={networkEnabled}
										onCheckedChange={setNetworkEnabled}
									/>
								</div>

								<div className="space-y-4">
									{showSubdomain && (
										<div className="space-y-1.5">
											<Label htmlFor="dw-subdomain" className="text-sm">
												Subdomain
											</Label>
											<div className="flex gap-2 items-center">
												<Input
													id="dw-subdomain"
													value={subdomain}
													onChange={(e) => {
														setSubdomain(
															e.target.value
																.toLowerCase()
																.replace(/[^a-z0-9-]/g, ""),
														);
													}}
													className="flex-1"
													placeholder="my-app"
													disabled={!networkEnabled}
												/>
												<div className="flex items-center h-9 px-3 bg-secondary rounded-md border text-xs text-muted-foreground shrink-0 whitespace-nowrap">
													.{platformDomain?.domain ?? "..."}
												</div>
												{subdomainAvailability && (
													<div className="flex items-center shrink-0 w-5">
														{subdomainAvailability === "checking" && (
															<Loader className="h-4 w-4 animate-spin text-muted-foreground" />
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
													This subdomain is not available.
												</p>
											)}
											{subdomain &&
												platformDomain &&
												subdomainAvailability !== "unavailable" && (
													<p className="text-xs text-muted-foreground">
														Your app will be publicly accessible at:{" "}
														<span className="font-mono">
															https://{subdomain}.{platformDomain.domain}
														</span>
													</p>
												)}
										</div>
									)}

									<div className="space-y-1.5">
										<Label htmlFor="dw-app-port" className="text-sm">
											Application Port
										</Label>
										<Input
											id="dw-app-port"
											type="number"
											placeholder="3000"
											value={port}
											onChange={(e) => {
												setPort(e.target.value);
											}}
											className="w-28"
											min="1"
											max="65535"
											disabled={!networkEnabled}
										/>
										<p className="text-xs text-muted-foreground">
											The port your container listens on
										</p>
									</div>
								</div>
							</div>
						)}

						{/* Step 3: Resources */}
						{step === 3 && (
							<div className="space-y-5">
								<p className="text-sm font-medium text-foreground">Resources</p>

								<div className="space-y-1.5">
									<Label htmlFor="dw-region" className="text-sm">
										Region
									</Label>
									<Select
										value={region}
										onValueChange={(value) => {
											// Base UI emits null when cleared; region is required
											// and always has a default, so keep the current value.
											if (value !== null) setRegion(value);
										}}
									>
										<SelectTrigger id="dw-region">
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
								</div>

								<div className="grid grid-cols-2 gap-6">
									<div className="space-y-3">
										<div className="flex items-center justify-between">
											<Label className="text-xs text-muted-foreground">
												CPU
											</Label>
											<span className="text-sm font-semibold tabular-nums">
												{CPU_OPTIONS[cpuIndex]} vCPU
											</span>
										</div>
										<Slider
											value={[cpuIndex]}
											onValueChange={(v) => {
												setCpuIndex(v[0]);
											}}
											min={0}
											max={CPU_OPTIONS.length - 1}
											step={1}
										/>
										<div className="flex justify-between text-xs text-muted-foreground">
											<span>{CPU_OPTIONS[0]} vCPU</span>
											<span>{CPU_OPTIONS[CPU_OPTIONS.length - 1]} vCPU</span>
										</div>
									</div>

									<div className="space-y-3">
										<div className="flex items-center justify-between">
											<Label className="text-xs text-muted-foreground">
												Memory
											</Label>
											<span className="text-sm font-semibold tabular-nums">
												{MEMORY_OPTIONS[memIndex]}
											</span>
										</div>
										<Slider
											value={[memIndex]}
											onValueChange={(v) => {
												setMemIndex(v[0]);
											}}
											min={0}
											max={MEMORY_OPTIONS.length - 1}
											step={1}
										/>
										<div className="flex justify-between text-xs text-muted-foreground">
											<span>{MEMORY_OPTIONS[0]}</span>
											<span>{MEMORY_OPTIONS[MEMORY_OPTIONS.length - 1]}</span>
										</div>
									</div>
								</div>
							</div>
						)}

						{/* Step 4: Environment */}
						{step === 4 && (
							<div className="space-y-4">
								<div className="flex items-start justify-between gap-4">
									<div>
										<p className="text-sm font-medium text-foreground">
											Environment Variables
										</p>
										<p className="text-xs text-muted-foreground mt-0.5">
											Configure runtime secrets and config for your app
										</p>
									</div>
									<div className="flex gap-2 shrink-0">
										<Button
											type="button"
											variant="outline"
											size="sm"
											onClick={() => {
												setIsEnvModalOpen(true);
											}}
											className="h-8"
										>
											<FileText className="h-3.5 w-3.5 mr-1.5" />
											Import .env
										</Button>
										<Button
											type="button"
											variant="outline"
											size="sm"
											onClick={addEnvVar}
											className="h-8"
										>
											<Plus className="h-3.5 w-3.5 mr-1.5" />
											Add
										</Button>
									</div>
								</div>

								{envVars.length === 0 ? (
									<div className="flex flex-col items-center justify-center py-10 gap-1.5 border border-dashed rounded-lg text-center">
										<p className="text-sm text-muted-foreground">
											No environment variables
										</p>
										<p className="text-xs text-muted-foreground/70">
											Add variables manually or import from a .env file
										</p>
									</div>
								) : (
									<div className="space-y-2">
										<div className="grid grid-cols-[1fr_1fr_2.25rem] gap-2 px-1">
											<span className="text-xs text-muted-foreground font-medium">
												Key
											</span>
											<span className="text-xs text-muted-foreground font-medium">
												Value
											</span>
											<span />
										</div>
										{envVars.map((env, index) => {
											const isRevealed = revealedVars.has(index);
											const countdown = revealCountdowns.get(index);
											return (
												<div
													key={env.id}
													className="grid grid-cols-[1fr_1fr_2.25rem] gap-2 items-center"
												>
													<Input
														placeholder="KEY"
														value={env.key}
														onChange={(e) => {
															updateEnvVar(index, "key", e.target.value);
														}}
														className="font-mono text-sm h-9"
													/>
													<div className="relative">
														<Input
															placeholder="value"
															type={isRevealed ? "text" : "password"}
															value={env.value}
															onChange={(e) => {
																updateEnvVar(index, "value", e.target.value);
															}}
															className="font-mono text-xs h-8 pr-14"
														/>
														<button
															type="button"
															onClick={() => {
																if (isRevealed) hideVar(index);
																else revealVar(index);
															}}
															className="absolute right-2 top-1/2 -translate-y-1/2 flex items-center gap-1 text-muted-foreground hover:text-foreground transition-colors"
														>
															{isRevealed ? (
																<>
																	<span className="text-xs tabular-nums text-amber-500 font-medium">
																		{countdown}s
																	</span>
																	<EyeOff className="h-3.5 w-3.5" />
																</>
															) : (
																<Eye className="h-3.5 w-3.5" />
															)}
														</button>
													</div>
													<Button
														type="button"
														variant="ghost"
														size="sm"
														onClick={() => {
															removeEnvVar(index);
														}}
														className="h-8 w-8 p-0 text-muted-foreground hover:text-destructive"
													>
														<Trash2 className="h-3.5 w-3.5" />
													</Button>
												</div>
											);
										})}
									</div>
								)}
							</div>
						)}
					</div>

					{/* Footer */}
					<div className="px-6 py-4 border-t border-border flex items-center justify-between">
						<Button
							type="button"
							variant="secondary"
							onClick={() => {
								if (step === 1) onClose();
								else setStep(step - 1);
							}}
							disabled={isSubmitting}
						>
							{step === 1 ? "Cancel" : "Back"}
						</Button>
						<Button
							type="button"
							onClick={handleNext}
							disabled={
								isSubmitting ||
								(step === 1 && !canProceedStep1) ||
								(step === 2 && !canProceedStep2)
							}
						>
							{isSubmitting ? (
								<>
									<Loader className="h-4 w-4 animate-spin mr-2" />
									Deploying...
								</>
							) : step < 4 ? (
								"Continue"
							) : (
								submitLabel
							)}
						</Button>
					</div>
				</DialogContent>
			</Dialog>

			{/* .env import sub-dialog */}
			<Dialog open={isEnvModalOpen} onOpenChange={setIsEnvModalOpen}>
				<DialogContent className="max-w-lg">
					<DialogHeader>
						<DialogTitle className="flex items-center gap-2">
							<FileText className="h-4 w-4" />
							Import .env
						</DialogTitle>
						<DialogDescription>
							Paste your .env contents. Variables will replace the current list.
						</DialogDescription>
					</DialogHeader>
					<div className="rounded-lg border border-border overflow-hidden">
						<div className="flex items-center justify-between px-3 py-2 bg-muted/50 border-b border-border">
							<span className="text-xs font-mono text-muted-foreground">
								.env
							</span>
							{envFileContent.trim() && (
								<span className="text-xs text-muted-foreground">
									{String(parseEnvFile(envFileContent).length)} variable
									{parseEnvFile(envFileContent).length !== 1 ? "s" : ""}{" "}
									detected
								</span>
							)}
						</div>
						<Textarea
							value={envFileContent}
							onChange={(e) => {
								setEnvFileContent(e.target.value);
							}}
							placeholder={
								"DATABASE_URL=postgresql://user:pass@localhost/db\nAPI_KEY=your-api-key\nNODE_ENV=production\n# comments are supported"
							}
							className="font-mono text-xs min-h-[200px] resize-none border-0 rounded-none focus-visible:ring-0 bg-transparent"
							spellCheck={false}
							autoFocus
						/>
					</div>
					{parseEnvFile(envFileContent).length > 0 && (
						<div className="flex flex-wrap gap-1.5">
							{parseEnvFile(envFileContent)
								.slice(0, 6)
								.map((v) => (
									<span
										key={v.key}
										className="text-xs font-mono bg-muted px-2 py-0.5 rounded text-muted-foreground"
									>
										{v.key}
									</span>
								))}
							{parseEnvFile(envFileContent).length > 6 && (
								<span className="text-xs text-muted-foreground self-center">
									+{String(parseEnvFile(envFileContent).length - 6)} more
								</span>
							)}
						</div>
					)}
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
							disabled={parseEnvFile(envFileContent).length === 0}
						>
							Import
						</Button>
					</DialogFooter>
				</DialogContent>
			</Dialog>
		</>
	);
}
