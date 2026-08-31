import Loader from "@/assets/loader.svg?react";
import {
	DeploymentWizard,
	type DeploymentWizardValues,
} from "@/components/DeploymentWizard";
import { Card, CardContent } from "@/components/design/Card";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { createDeployment } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import {
	DeploymentPhase,
	type Deployment,
} from "@gen/loco/deployment/v1/deployment_pb";
import { RegionIntentStatus } from "@gen/loco/resource/v1/resource_pb";
import { useResourceDetails } from "@/hooks/useResourceDetails";
import { getStatusLabel } from "@/lib/app-status";
import { getServiceSpec } from "@/lib/deployment-utils";
import { getErrorMessage } from "@/lib/error-handler";
import { subscribeToEvents } from "@/lib/events";
import { relativeTime } from "@/lib/format-time";
import { cn, lookupEnum, nonEmpty } from "@/lib/utils";
import { useMutation } from "@connectrpc/connect-query";
import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { toast } from "sonner";

import { ActivitySheet } from "./resource-details/ActivitySheet";
import { ArchModal } from "./resource-details/ArchDiagram";
import { SettingsSheet } from "./resource-details/SettingsSheet";
import { SpecDiffModal } from "./resource-details/SpecDiffModal";
import {
	deploymentImage,
	parseCpuMilli,
	parseMemMi,
	shortId,
} from "./resource-details/format";
import { mockUsagePct } from "./resource-details/mock-usage";
import {
	PHASE_CFG,
	STATUS_CFG,
	statusKeyFromLabel,
} from "./resource-details/status";

export function ResourceDetails() {
	const { resourceId } = useParams<{ resourceId: string }>();
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const navigate = useNavigate();

	const {
		resource: resourceResponse,
		deployments,
		isLoading,
		error,
	} = useResourceDetails(resourceId ?? "");
	const resource = resourceResponse?.resource;

	const [diff, setDiff] = useState<{
		current: Deployment;
		old: Deployment;
	} | null>(null);
	const [activityOpen, setActivityOpen] = useState(false);
	const [settingsOpen, setSettingsOpen] = useState(false);
	const [archOpen, setArchOpen] = useState(false);
	const [deployDialogOpen, setDeployDialogOpen] = useState(false);
	const [rollingBackId, setRollingBackId] = useState<string | null>(null);

	const anySheetOpen = activityOpen || settingsOpen;

	const redeployMutation = useMutation(createDeployment);
	const deployMutation = useMutation(createDeployment);

	useEffect(() => {
		if (!resourceId) return;
		const unsub = subscribeToEvents(`resource:${resourceId}`, () => {
			/* no-op */
		});
		return unsub;
	}, [resourceId]);

	// ── loading / error states ───────────────────────────────────────────────
	if (!resourceId) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-medium mb-2">
							Invalid Resource ID
						</p>
						<p className="text-sm text-muted-foreground">
							The resource ID is missing from the URL
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	if (isLoading) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<div className="inline-flex flex-col gap-2 items-center">
					<Loader className="w-8 h-8" />
					<p className="text-sm text-muted-foreground">Loading resource…</p>
				</div>
			</div>
		);
	}

	if (error) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-medium mb-4">
							Error Loading Resource
						</p>
						<p className="text-sm text-muted-foreground">
							{getErrorMessage(error, "Failed to load resource")}
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	if (!resource) {
		return (
			<div className="flex items-center justify-center min-h-96">
				<Card className="max-w-md">
					<CardContent className="p-6 text-center">
						<p className="text-destructive font-medium mb-2">
							Resource Not Found
						</p>
						<p className="text-sm text-muted-foreground">
							The resource with ID {resourceId} does not exist
						</p>
					</CardContent>
				</Card>
			</div>
		);
	}

	// ── derived data ─────────────────────────────────────────────────────────
	const statusKey = statusKeyFromLabel(getStatusLabel(resource.status));
	const st = STATUS_CFG[statusKey];
	const activeDep = deployments.find((d) => d.isActive) ?? deployments[0];
	const primaryDomain = resource.domains[0]?.domain;
	const activeSvc = activeDep ? getServiceSpec(activeDep) : undefined;

	const handleRedeploy = async () => {
		if (!activeDep?.spec) {
			toast.error("No active deployment to redeploy");
			return;
		}
		try {
			await redeployMutation.mutateAsync({
				resourceId,
				region: activeDep.region,
				spec: activeDep.spec,
				environmentId: activeDep.environmentId,
			});
			toast.success("Redeployment started");
		} catch {
			toast.error("Failed to trigger redeployment");
		}
	};

	const handleRollback = async (dep: Deployment) => {
		if (!dep.spec) return;
		setRollingBackId(dep.id);
		try {
			await redeployMutation.mutateAsync({
				resourceId,
				region: dep.region,
				spec: dep.spec,
				environmentId: dep.environmentId,
			});
			toast.success("Rollback started");
		} catch {
			toast.error("Rollback failed");
		} finally {
			setRollingBackId(null);
		}
	};

	const handleDeploy = async (values: DeploymentWizardValues) => {
		try {
			await deployMutation.mutateAsync({
				resourceId,
				region: values.region,
				spec: {
					spec: {
						case: "service",
						value: {
							build: { type: "image", image: values.imageUrl },
							cpu: values.cpu,
							memory: values.memory,
							minReplicas: values.replicas,
							maxReplicas: values.replicas,
							port: values.port,
							env: values.envVars,
						},
					},
				},
				...(deployments[0]?.environmentId
					? { environmentId: deployments[0].environmentId }
					: {}),
			});
			setDeployDialogOpen(false);
			toast.success("Deployment started");
		} catch {
			toast.error("Failed to start deployment");
		}
	};

	const redeploying = redeployMutation.isPending;

	// ── per-region card data ─────────────────────────────────────────────────
	const regionCards = resource.regions.map((r, idx) => {
		const rDep =
			deployments.find((d) => d.region === r.region && d.isActive) ?? activeDep;
		const rSvc = rDep ? getServiceSpec(rDep) : activeSvc;
		const statusKey2 = (() => {
			switch (r.status) {
				case RegionIntentStatus.PROVISIONING:
					return "deploying";
				case RegionIntentStatus.ACTIVE:
					return "healthy";
				case RegionIntentStatus.DEGRADED:
					return "degraded";
				case RegionIntentStatus.FAILED:
					return "failed";
				case RegionIntentStatus.REMOVING:
					return "suspended";
				case RegionIntentStatus.DESIRED:
					return "pending";
				case RegionIntentStatus.UNSPECIFIED:
					return "pending";
			}
		})();
		const rs = STATUS_CFG[statusKey2];

		const cpuLimit = parseCpuMilli(rSvc?.cpu ?? "500m");
		const memLimit = parseMemMi(rSvc?.memory ?? "512Mi");
		const cpuPct = mockUsagePct(statusKey2, idx * 3);
		const memPct = mockUsagePct(statusKey2, idx * 3 + 1);
		const cpuUsed = Math.round((cpuLimit * cpuPct) / 100);
		const memUsed = Math.round((memLimit * memPct) / 100);
		const replicas = rDep?.replicas ?? rSvc?.minReplicas ?? 1;

		return {
			region: r.region,
			isPrimary: r.isPrimary,
			rs,
			statusKey2,
			cpuUsed,
			cpuLimit,
			memUsed,
			memLimit,
			memPct,
			cpuPct,
			replicas,
		};
	});

	// ── render ───────────────────────────────────────────────────────────────
	return (
		<div
			className={cn(
				"font-sans text-foreground transition-[padding-right] duration-320 ease-[cubic-bezier(0.32,0.72,0,1)]",
				anySheetOpen ? "pr-[440px]" : "pr-0",
			)}
		>
			<div
				className={cn(
					"max-w-7xl mx-auto transition-[padding] duration-320 ease-[cubic-bezier(0.32,0.72,0,1)]",
					anySheetOpen ? "pt-7 px-4 pb-20" : "pt-7 px-10 pb-20",
				)}
			>
				{/* ── Header ── */}
				<div className="flex items-start justify-between flex-wrap gap-4 mb-7">
					<div>
						<div className="flex items-center gap-2.5 mb-2">
							<h1 className="font-serif text-[28px] font-normal tracking-[-0.3px] leading-[1.2] m-0">
								{resource.name}
							</h1>
							<span
								className="text-xs px-2.5 py-[3px] rounded-full font-semibold flex items-center gap-[5px] shrink-0"
								style={{ background: st.bg, color: st.color }}
							>
								<span
									className={cn(
										"w-[6px] h-[6px] rounded-full inline-block",
										statusKey !== "healthy" && "animate-status-pulse",
									)}
									style={{ background: st.dot }}
								/>
								{st.label}
							</span>
						</div>
						<div className="flex items-center gap-[7px] flex-wrap">
							{activeDep && (
								<span className="flex items-center gap-1 font-mono text-[11px] text-[#8a7a68] bg-[#ede7dd] px-2 py-[3px] rounded-[5px]">
									<svg
										width="9"
										height="9"
										viewBox="0 0 24 24"
										fill="none"
										stroke="currentColor"
										strokeWidth="2.5"
									>
										<rect x="3" y="3" width="18" height="18" rx="2" />
										<path d="M9 9h6M9 12h6M9 15h4" />
									</svg>
									{shortId(activeDep.id)}
								</span>
							)}
							{activeDep && deploymentImage(activeDep) !== "—" && (
								<span className="font-mono text-[11px] text-[#6258a0] bg-[#eeecf8] px-2 py-[3px] rounded-[5px] flex items-center gap-1">
									<svg
										width="10"
										height="10"
										viewBox="0 0 24 24"
										fill="none"
										stroke="currentColor"
										strokeWidth="2"
									>
										<rect x="2" y="7" width="20" height="14" rx="2" />
										<path d="M16 7V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v2" />
									</svg>
									{deploymentImage(activeDep).split("/").pop()}
								</span>
							)}
							{primaryDomain && (
								<a
									href={`https://${primaryDomain}`}
									target="_blank"
									rel="noopener noreferrer"
									className="font-mono text-[11px] text-[#4a6b9c]! bg-[#edf2f8] px-2 py-[3px] rounded-[5px] no-underline flex items-center gap-1"
								>
									<svg
										width="10"
										height="10"
										viewBox="0 0 24 24"
										fill="none"
										stroke="currentColor"
										strokeWidth="2"
									>
										<path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
										<polyline points="15 3 21 3 21 9" />
										<line x1="10" y1="14" x2="21" y2="3" />
									</svg>
									{primaryDomain}
								</a>
							)}
						</div>
					</div>

					<div className="flex gap-1.5 pt-1 items-center">
						<button
							className="inline-flex items-center gap-1.5 px-3.5 py-[7px] rounded-lg text-[13px] font-medium text-[#6b5d4f] border border-[#ddd5c8] bg-transparent hover:bg-[#f0ebe3] hover:border-[#c9bbad] transition-all cursor-pointer"
							onClick={() => {
								if (activeOrgId && activeWorkspaceId)
									void navigate(
										`/org/${activeOrgId}/wks/${activeWorkspaceId}/observability`,
									);
							}}
						>
							<svg
								width="13"
								height="13"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
							>
								<polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
							</svg>
							Telemetry
						</button>
						<button
							className="inline-flex items-center justify-center w-[34px] h-[34px] rounded-lg cursor-pointer transition-all bg-transparent border border-[#ddd5c8] hover:bg-[#f0ebe3] hover:border-[#c9bbad] text-[#6b5d4f]"
							title="Architecture"
							onClick={() => {
								setArchOpen(true);
							}}
						>
							<svg
								width="14"
								height="14"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
							>
								<rect x="3" y="3" width="7" height="7" rx="1" />
								<rect x="14" y="3" width="7" height="7" rx="1" />
								<rect x="3" y="14" width="7" height="7" rx="1" />
								<path d="M17.5 14v3m0 3v.01M17.5 17h3m-6 0h.01" />
							</svg>
						</button>
						<button
							className={cn(
								"inline-flex items-center justify-center w-[34px] h-[34px] rounded-lg cursor-pointer transition-all bg-transparent border border-[#ddd5c8] hover:bg-[#f0ebe3] hover:border-[#c9bbad]",
								activityOpen
									? "bg-[#f0ebe3] border-[#c9bbad] text-foreground"
									: "text-[#6b5d4f]",
							)}
							title="Activity"
							onClick={() => {
								setActivityOpen((v) => !v);
								setSettingsOpen(false);
							}}
						>
							<svg
								width="14"
								height="14"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
							>
								<path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
								<path d="M13.73 21a2 2 0 0 1-3.46 0" />
							</svg>
						</button>
						<button
							className={cn(
								"inline-flex items-center justify-center w-[34px] h-[34px] rounded-lg cursor-pointer transition-all bg-transparent border border-[#ddd5c8] hover:bg-[#f0ebe3] hover:border-[#c9bbad]",
								settingsOpen
									? "bg-[#f0ebe3] border-[#c9bbad] text-foreground"
									: "text-[#6b5d4f]",
							)}
							title="Settings"
							onClick={() => {
								setSettingsOpen((v) => !v);
								setActivityOpen(false);
							}}
						>
							<svg
								width="14"
								height="14"
								viewBox="0 0 24 24"
								fill="none"
								stroke="currentColor"
								strokeWidth="2"
							>
								<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
							</svg>
						</button>
						{activeDep ? (
							<button
								className={cn(
									"inline-flex items-center gap-1.5 px-3.5 py-[7px] rounded-lg bg-[#2a2018] text-[#f7f3ec] text-[13px] font-medium border-none transition-all",
									redeploying
										? "opacity-55 cursor-not-allowed"
										: "cursor-pointer hover:bg-[#3d2f20]",
								)}
								onClick={() => {
									void handleRedeploy();
								}}
								disabled={redeploying}
							>
								{redeploying ? (
									<svg
										className="animate-spin"
										width="13"
										height="13"
										viewBox="0 0 24 24"
										fill="none"
										stroke="currentColor"
										strokeWidth="2"
									>
										<path d="M21 12a9 9 0 1 1-6.219-8.56" />
									</svg>
								) : (
									<svg
										width="13"
										height="13"
										viewBox="0 0 24 24"
										fill="none"
										stroke="currentColor"
										strokeWidth="2"
									>
										<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
										<path d="M3 3v5h5" />
									</svg>
								)}
								{redeploying ? "Deploying…" : "Redeploy"}
							</button>
						) : (
							<button
								className="inline-flex items-center gap-1.5 px-3.5 py-[7px] rounded-lg bg-[#2a2018] text-[#f7f3ec] text-[13px] font-medium border-none cursor-pointer hover:bg-[#3d2f20] transition-all"
								onClick={() => {
									setDeployDialogOpen(true);
								}}
							>
								<svg
									width="13"
									height="13"
									viewBox="0 0 24 24"
									fill="none"
									stroke="currentColor"
									strokeWidth="2"
								>
									<path d="M12 5v14M5 12l7-7 7 7" />
								</svg>
								Deploy
							</button>
						)}
					</div>
				</div>

				{/* ── Resource cards ── */}
				{regionCards.length === 1 ? (
					/* Single region: expanded 3-card layout */
					(() => {
						const r = regionCards[0];
						if (!r) return null;
						return (
							<div className="grid grid-cols-3 gap-3 mb-5">
								{/* Replicas */}
								<div className="bg-card border border-[#e8e0d4] rounded-xl p-5">
									<div className="text-[11px] font-semibold text-[#a0907e] tracking-[0.08em] uppercase mb-2.5">
										Replicas
									</div>
									<div className="flex items-baseline gap-1">
										<span className="text-[28px] font-semibold font-serif text-foreground">
											{r.replicas}
										</span>
									</div>
									<div className="flex gap-[5px] mt-2.5 flex-wrap">
										{Array.from({ length: Math.min(r.replicas, 12) }).map(
											(_, i) => (
												<div
													key={i}
													className="w-2 h-2 rounded-full"
													style={{ background: r.rs.dot }}
												/>
											),
										)}
									</div>
								</div>

								{/* CPU */}
								<div className="bg-card border border-[#e8e0d4] rounded-xl p-5">
									<div className="text-[11px] font-semibold text-[#a0907e] tracking-[0.08em] uppercase mb-2.5">
										CPU
									</div>
									<div className="flex items-baseline gap-1">
										<span
											className={cn(
												"text-[28px] font-semibold font-serif",
												r.cpuPct > 80 ? "text-[#8b2e2e]" : "text-foreground",
											)}
										>
											{r.cpuUsed}
										</span>
										<span className="text-sm text-[#a0907e]">
											m / {r.cpuLimit}m
										</span>
									</div>
									<div className="mt-3">
										<div className="h-[3px] bg-[#e8e0d4] rounded-xs overflow-hidden">
											<div
												className="h-full rounded-xs transition-[width] duration-600"
												style={{
													width: `${r.cpuPct}%`,
													background:
														r.cpuPct > 80
															? "#c0392b"
															: r.cpuPct > 60
																? "#d4870a"
																: "#c4956a",
												}}
											/>
										</div>
										<div className="text-[11px] text-[#a0907e] mt-1">
											{r.cpuPct}% of limit
										</div>
									</div>
								</div>

								{/* Memory */}
								<div className="bg-card border border-[#e8e0d4] rounded-xl p-5">
									<div className="text-[11px] font-semibold text-[#a0907e] tracking-[0.08em] uppercase mb-2.5">
										Memory
									</div>
									<div className="flex items-baseline gap-1">
										<span
											className={cn(
												"text-[28px] font-semibold font-serif",
												r.memPct > 80 ? "text-[#8b2e2e]" : "text-foreground",
											)}
										>
											{r.memUsed}
										</span>
										<span className="text-sm text-[#a0907e]">
											MB / {r.memLimit}MB
										</span>
									</div>
									<div className="mt-3">
										<div className="h-[3px] bg-[#e8e0d4] rounded-xs overflow-hidden">
											<div
												className="h-full rounded-xs transition-[width] duration-600"
												style={{
													width: `${r.memPct}%`,
													background:
														r.memPct > 80
															? "#c0392b"
															: r.memPct > 60
																? "#d4870a"
																: "#4a7c59",
												}}
											/>
										</div>
										<div className="text-[11px] text-[#a0907e] mt-1">
											{r.memPct}% of limit
										</div>
									</div>
								</div>
							</div>
						);
					})()
				) : regionCards.length > 1 ? (
					/* Multi-region: one compact card per region */
					<div
						className="grid gap-2.5 mb-5"
						style={{
							gridTemplateColumns: `repeat(${Math.min(regionCards.length, 4)}, 1fr)`,
						}}
					>
						{regionCards.map((r) => (
							<div
								key={r.region}
								className={cn(
									"bg-card border rounded-xl px-[18px] py-4",
									r.statusKey2 === "degraded"
										? "border-[#e8d4a4]"
										: r.statusKey2 === "failed"
											? "border-[#e8c4c4]"
											: "border-[#e8e0d4]",
								)}
							>
								<div className="flex items-center justify-between mb-3">
									<div className="text-xs font-semibold text-[#4a3c30] flex items-center gap-[5px]">
										{r.region}
										{r.isPrimary && (
											<span className="text-[9px] text-[#b0a090] font-normal">
												primary
											</span>
										)}
									</div>
									<div className="flex items-center gap-1">
										<span
											className="w-[5px] h-[5px] rounded-full"
											style={{ background: r.rs.dot }}
										/>
										<span
											className="text-[11px] font-medium"
											style={{ color: r.rs.color }}
										>
											{r.rs.label}
										</span>
									</div>
								</div>
								<div className="flex flex-col gap-2.5">
									<div>
										<div className="flex justify-between mb-1">
											<span className="text-[10px] text-[#a0907e] uppercase tracking-[0.07em] font-semibold">
												CPU
											</span>
											<span
												className={cn(
													"font-mono text-[10px]",
													r.cpuPct > 70 ? "text-[#9c6b1e]" : "text-[#6b5d4f]",
												)}
											>
												{r.cpuUsed}m / {r.cpuLimit}m
											</span>
										</div>
										<div className="h-[3px] bg-[#e8e0d4] rounded-[2px] overflow-hidden">
											<div
												className="h-full rounded-[2px] transition-[width] duration-[600ms]"
												style={{
													width: `${r.cpuPct}%`,
													background:
														r.cpuPct > 80
															? "#c0392b"
															: r.cpuPct > 60
																? "#d4870a"
																: "#c4956a",
												}}
											/>
										</div>
									</div>
									<div>
										<div className="flex justify-between mb-1">
											<span className="text-[10px] text-[#a0907e] uppercase tracking-[0.07em] font-semibold">
												Memory
											</span>
											<span
												className={cn(
													"font-mono text-[10px]",
													r.memPct > 70 ? "text-[#9c6b1e]" : "text-[#6b5d4f]",
												)}
											>
												{r.memUsed}MB / {r.memLimit}MB
											</span>
										</div>
										<div className="h-[3px] bg-[#e8e0d4] rounded-[2px] overflow-hidden">
											<div
												className="h-full rounded-[2px] transition-[width] duration-[600ms]"
												style={{
													width: `${r.memPct}%`,
													background:
														r.memPct > 80
															? "#c0392b"
															: r.memPct > 60
																? "#d4870a"
																: "#4a7c59",
												}}
											/>
										</div>
									</div>
									<div className="flex justify-between pt-0.5">
										<span className="text-[10px] text-[#a0907e] uppercase tracking-[0.07em] font-semibold">
											Replicas
										</span>
										<div className="flex gap-[3px] items-center">
											{Array.from({ length: Math.min(r.replicas, 8) }).map(
												(_, i) => (
													<div
														key={i}
														className="w-[5px] h-[5px] rounded-full"
														style={{ background: r.rs.dot }}
													/>
												),
											)}
											<span className="font-mono text-[10px] text-[#8a7a68] ml-1">
												{r.replicas}
											</span>
										</div>
									</div>
								</div>
							</div>
						))}
					</div>
				) : null}

				{/* ── Deployments ── */}
				<div className="bg-card border border-[#e8e0d4] rounded-xl overflow-hidden">
					<div className="px-5 py-3 border-b border-[#ede7dd] flex items-center justify-between">
						<span className="font-serif text-[17px]">Deployments</span>
						<span className="text-[11px] text-[#a0907e]">
							Click any past deployment to compare spec
						</span>
					</div>

					<div
						className="grid gap-2.5 px-[26px] py-2 border-b border-[#f0e8dc]"
						style={{
							gridTemplateColumns:
								"minmax(80px,1fr) minmax(120px,2fr) 130px 110px 90px 110px",
						}}
					>
						{["ID", "Image", "Status", "Region", "Time", ""].map((h, i) => (
							<span
								key={i}
								className="text-[10px] font-semibold text-[#b0a090] uppercase tracking-[0.07em]"
							>
								{h}
							</span>
						))}
					</div>

					<div className="p-1.5 px-2">
						{deployments.length === 0 ? (
							<div className="p-7 text-center text-[#a0907e] text-[13px]">
								No deployments yet
							</div>
						) : (
							deployments.map((dep) => {
								const ph = lookupEnum(
									PHASE_CFG,
									dep.status,
									PHASE_CFG[DeploymentPhase.UNSPECIFIED],
								);
								const isCurr = dep.isActive;
								const canDiff = !isCurr && activeDep && activeDep.id !== dep.id;
								return (
									<div
										key={dep.id}
										className={cn(
											"group grid gap-2.5 items-center px-3.5 py-3 rounded-[10px] transition-colors border-l-[3px]",
											canDiff
												? "cursor-pointer hover:bg-[#f2ece2]"
												: "cursor-default",
											isCurr
												? "bg-[#f7f3ee] border-[#c4956a]"
												: "border-transparent",
										)}
										style={{
											gridTemplateColumns:
												"minmax(80px,1fr) minmax(120px,2fr) 130px 110px 90px 110px",
										}}
										onClick={() => {
											if (canDiff) setDiff({ current: activeDep, old: dep });
										}}
									>
										<span className="font-mono text-[11px] text-[#6b5d4f] overflow-hidden text-ellipsis whitespace-nowrap">
											{shortId(dep.id)}
										</span>
										<span className="font-mono text-[11px] text-[#6b5d4f] overflow-hidden text-ellipsis whitespace-nowrap">
											{nonEmpty(deploymentImage(dep).split("/").pop(), "—")}
										</span>
										<span
											className="text-[11px] px-[9px] py-[3px] rounded-[10px] font-semibold inline-flex items-center gap-[5px] w-fit"
											style={{ background: ph.bg, color: ph.color }}
										>
											{dep.status === DeploymentPhase.DEPLOYING && (
												<span className="animate-spin inline-block w-[7px] h-[7px] border-[1.5px] border-current border-t-transparent rounded-full" />
											)}
											{ph.label}
										</span>
										<span className="text-[11px] text-[#8a7a68] font-mono overflow-hidden text-ellipsis whitespace-nowrap">
											{dep.region || "—"}
										</span>
										<span className="text-[11px] text-[#a0907e]">
											{relativeTime(dep.createdAt)}
										</span>
										<div className="flex gap-1.5 items-center">
											{isCurr && (
												<span className="text-[10px] text-[#c4956a] font-semibold">
													current
												</span>
											)}
											{!isCurr && dep.status === DeploymentPhase.SUCCEEDED && (
												<button
													className={cn(
														"inline-flex items-center gap-1.5 px-[9px] py-[3px] rounded-lg text-[11px] font-medium text-[#6b5d4f] border border-[#ddd5c8] bg-transparent cursor-pointer transition-all hover:bg-[#f0ebe3] hover:border-[#c9bbad]",
														rollingBackId === dep.id &&
															"opacity-60 cursor-not-allowed",
													)}
													disabled={rollingBackId === dep.id}
													onClick={(e) => {
														e.stopPropagation();
														void handleRollback(dep);
													}}
												>
													{rollingBackId === dep.id
														? "Rolling back…"
														: "Rollback"}
												</button>
											)}
											{canDiff && (
												<span className="opacity-0 group-hover:opacity-100 transition-opacity text-[11px] text-[#c4956a] font-medium">
													diff →
												</span>
											)}
										</div>
									</div>
								);
							})
						)}
					</div>
				</div>
			</div>

			<ActivitySheet
				open={activityOpen}
				onClose={() => {
					setActivityOpen(false);
				}}
				resourceId={resourceId}
			/>

			<SettingsSheet
				open={settingsOpen}
				onClose={() => {
					setSettingsOpen(false);
				}}
				resourceId={resourceId}
				resourceName={resource.name}
				domains={resource.domains}
				activeDep={activeDep}
			/>

			{archOpen && (
				<ArchModal
					resourceName={resource.name}
					onClose={() => {
						setArchOpen(false);
					}}
				/>
			)}

			{diff && (
				<SpecDiffModal
					current={diff.current}
					old={diff.old}
					onClose={() => {
						setDiff(null);
					}}
				/>
			)}

			<DeploymentWizard
				open={deployDialogOpen}
				onClose={() => {
					setDeployDialogOpen(false);
				}}
				title="Deploy"
				submitLabel="Deploy"
				onSubmit={handleDeploy}
				isSubmitting={deployMutation.isPending}
			/>
		</div>
	);
}
