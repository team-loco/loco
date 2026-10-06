import { createClient } from "@connectrpc/connect";
import { useMutation, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { BoxIcon, CircleAlertIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { DeploymentPhase, DeploymentService } from "@gen/loco/deployment/v1/deployment_pb";
import { createDeployment } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DomainType } from "@gen/loco/domain/v1/domain_pb";
import { checkDomainAvailability, listPlatformDomains } from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { ResourceType, type RegionInfo } from "@gen/loco/resource/v1/resource_pb";
import { createResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { useNow } from "@/hooks/useNow";
import { getErrorMessage } from "@/lib/error-handler";
import { resourcePath } from "@/lib/routes";
import { maybeTsMs } from "@/lib/time";
import { cn } from "@/lib/utils";

import { DraftForm } from "./DraftForm";
import { DraftProgress, progressSteps } from "./DraftProgress";
import { NAME_RE, type Draft } from "./drafts";
import { firstError, validateDraft } from "./draftValidation";

function useDebounced<T>(value: T, ms: number): T {
	const [debounced, setDebounced] = useState(value);
	useEffect(() => {
		const id = setTimeout(() => {
			setDebounced(value);
		}, ms);
		return () => {
			clearTimeout(id);
		};
	}, [value, ms]);
	return debounced;
}

function useDeploymentWatch(deploymentId: string | undefined) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const [state, setState] = useState<{
		id: string;
		phase: DeploymentPhase;
		message: string;
		at: number;
	} | null>(null);
	useEffect(() => {
		if (deploymentId === undefined) return;
		const controller = new AbortController();
		const client = createClient(DeploymentService, transport);
		const run = async () => {
			try {
				for await (const ev of client.watchDeployment({ deploymentId }, { signal: controller.signal })) {
					const at = maybeTsMs(ev.timestamp) ?? Date.now();
					setState({ id: deploymentId, phase: ev.status, message: ev.message, at });
					void queryClient.invalidateQueries();
					if (ev.status === DeploymentPhase.RUNNING) break;
				}
			} catch {
				return;
			}
		};
		void run();
		return () => {
			controller.abort();
		};
	}, [deploymentId, transport, queryClient]);
	if (state === null || state.id !== deploymentId) return { phase: undefined, message: "", at: 0 };
	return state;
}

function terminal(phase: DeploymentPhase | undefined): "live" | "failed" | null {
	switch (phase) {
		case DeploymentPhase.RUNNING:
			return "live";
		case DeploymentPhase.SUCCEEDED:
			return "live";
		case DeploymentPhase.FAILED:
			return "failed";
		case DeploymentPhase.CANCELED:
			return "failed";
		case DeploymentPhase.PENDING:
			return null;
		case DeploymentPhase.DEPLOYING:
			return null;
		case DeploymentPhase.UNSPECIFIED:
			return null;
		case undefined:
			return null;
	}
}

function formatElapsed(ms: number): string {
	const s = Math.max(0, Math.round(ms / 1000));
	return s < 60 ? `${s.toString()}s` : `${Math.floor(s / 60).toString()}m ${(s % 60).toString()}s`;
}

export function DraftDrawer({
	draft,
	open,
	onOpenChange,
	orgId,
	workspaceId,
	env,
	regions,
	otherSubs,
	update,
	remove,
}: {
	draft: Draft | undefined;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	orgId: string;
	workspaceId: string;
	env: Environment;
	regions: RegionInfo[];
	otherSubs: Set<string>;
	update: (id: string, patch: Partial<Draft>) => void;
	remove: (id: string) => void;
}) {
	return (
		<Sheet open={open && draft !== undefined} onOpenChange={onOpenChange}>
			<SheetContent>
				{draft !== undefined && (
					<DraftDrawerBody
						key={draft.id}
						draft={draft}
						orgId={orgId}
						workspaceId={workspaceId}
						env={env}
						regions={regions}
						otherSubs={otherSubs}
						update={(patch) => {
							update(draft.id, patch);
						}}
						onClose={() => {
							onOpenChange(false);
						}}
						remove={() => {
							remove(draft.id);
						}}
					/>
				)}
			</SheetContent>
		</Sheet>
	);
}

function DraftDrawerBody({
	draft,
	orgId,
	workspaceId,
	env,
	regions,
	otherSubs,
	update,
	onClose,
	remove,
}: {
	draft: Draft;
	orgId: string;
	workspaceId: string;
	env: Environment;
	regions: RegionInfo[];
	otherSubs: Set<string>;
	update: (patch: Partial<Draft>) => void;
	onClose: () => void;
	remove: () => void;
}) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const [tried, setTried] = useState(false);
	const [healthOpen, setHealthOpen] = useState(false);
	const [submitting, setSubmitting] = useState(false);
	const [error, setError] = useState<string | null>(null);

	const { data: configRes } = useQuery(getConfig, {});
	const { data: domainsRes } = useQuery(listPlatformDomains, { activeOnly: true });
	const config = configRes?.serviceDefaults;
	const platformDomains = domainsRes?.platformDomains ?? [];
	const platform = platformDomains.find((d) => d.domain === config?.platformDomain) ?? platformDomains[0];
	const platformDomain = platform?.domain ?? config?.platformDomain ?? "onloco.app";

	const defaultRegion = regions.find((r) => r.isDefault)?.region ?? regions[0]?.region ?? "";
	const region = regions.some((r) => r.region === draft.region) ? draft.region : defaultRegion;

	const sub = draft.sub.trim();
	const debouncedSub = useDebounced(sub, 350);
	const subLooksValid = NAME_RE.test(debouncedSub) && !otherSubs.has(debouncedSub);
	const availabilityQuery = useQuery(
		checkDomainAvailability,
		{ domain: `${debouncedSub}.${platformDomain}` },
		{ enabled: subLooksValid && draft.resourceId === undefined, staleTime: 10_000 },
	);
	const availability =
		debouncedSub !== sub || availabilityQuery.isFetching
			? "checking"
			: availabilityQuery.data === undefined
				? "unknown"
				: availabilityQuery.data.isAvailable
					? "available"
					: "taken";

	const errors = validateDraft(draft, region, platformDomain, otherSubs, availability);
	const blocker = firstError(errors);

	const createResourceMutation = useMutation(createResource);
	const createDeploymentMutation = useMutation(createDeployment);

	const watch = useDeploymentWatch(draft.deploymentId);
	const done = terminal(watch.phase);
	const inProgress = submitting || draft.resourceId !== undefined;
	const now = useNow(inProgress && done === null ? 1000 : undefined);

	const startDeployment = async (resourceId: string) => {
		const autoscale = draft.min !== draft.max;
		const env_vars: Record<string, string> = {};
		for (const v of draft.vars) if (v.key.trim() !== "") env_vars[v.key.trim()] = v.value;
		const res = await createDeploymentMutation.mutateAsync({
			resourceId,
			region,
			environmentId: env.id,
			spec: {
				spec: {
					case: "service",
					value: {
						build: { type: "image", image: draft.image },
						cpu: draft.cpu,
						memory: draft.memory,
						minReplicas: draft.min,
						maxReplicas: draft.max,
						...(autoscale ? { scalers: { enabled: true, cpuTarget: Number(draft.cpuTarget) } } : {}),
						env: env_vars,
						port: Number(draft.port),
						healthCheck: {
							path: draft.hcPath,
							intervalSeconds: Number(draft.hcInterval),
							timeoutSeconds: Number(draft.hcTimeout),
							failureThreshold: Number(draft.hcFail),
							initialDelaySeconds: 0,
						},
					},
				},
			},
		});
		update({ deploymentId: res.deploymentId });
	};

	const deploy = async () => {
		if (blocker !== null) {
			setTried(true);
			if (errors.health !== null) setHealthOpen(true);
			return;
		}
		setSubmitting(true);
		setError(null);
		update({ region, deployedAt: Date.now() });
		let resourceId = draft.resourceId;
		try {
			if (resourceId === undefined) {
				const autoscale = draft.min !== draft.max;
				const res = await createResourceMutation.mutateAsync({
					workspaceId,
					name: draft.name,
					type: ResourceType.SERVICE,
					domain: {
						domainSource: DomainType.PLATFORM_PROVIDED,
						subdomain: sub,
						...(platform !== undefined ? { platformDomainId: platform.id } : {}),
					},
					spec: {
						spec: {
							case: "service",
							value: {
								routing: { port: Number(draft.port), pathPrefix: "/", idleTimeout: config?.routing?.idleTimeout ?? 60 },
								...(config?.observability !== undefined ? { observability: config.observability } : {}),
								regions: {
									[region]: {
										enabled: true,
										primary: true,
										cpu: draft.cpu,
										memory: draft.memory,
										minReplicas: draft.min,
										maxReplicas: draft.max,
										...(autoscale
											? { scalers: { enabled: true, cpuTarget: Number(draft.cpuTarget) } }
											: {}),
									},
								},
								healthCheck: {
									path: draft.hcPath,
									intervalSeconds: Number(draft.hcInterval),
									timeoutSeconds: Number(draft.hcTimeout),
									failureThreshold: Number(draft.hcFail),
									initialDelaySeconds: 0,
								},
							},
						},
					},
				});
				resourceId = res.resourceId;
				update({ resourceId });
			}
			await startDeployment(resourceId);
		} catch (err) {
			const message = getErrorMessage(err, "Deployment failed");
			setError(message);
			if (resourceId === undefined) {
				update({ deployedAt: undefined });
				setTried(true);
			}
		} finally {
			setSubmitting(false);
			void queryClient.invalidateQueries();
		}
	};

	const stalled =
		draft.resourceId !== undefined && draft.deploymentId === undefined && !submitting && error === null;
	const shownError = stalled ? "Deployment was not started" : error;
	const showForm = !inProgress;
	const statusBadge = showForm
		? { label: "Draft", tone: "muted" as const }
		: done === "live"
			? { label: "Healthy", tone: "ok" as const }
			: done === "failed" || (error !== null && !submitting) || stalled
				? { label: "Failed", tone: "bad" as const }
				: { label: "Deploying", tone: "info" as const };

	const steps = progressSteps({
		draft,
		region,
		envName: env.name,
		phase: watch.phase,
		message: watch.message,
		error: shownError,
		submitting,
	});
	const startedAt = draft.deployedAt ?? now.getTime();
	const endAt = done !== null ? watch.at : now.getTime();
	const progTitle =
		done === "live"
			? `Live at https://${sub}.${platformDomain}`
			: done === "failed" || (shownError !== null && !submitting)
				? "Deployment failed"
				: "Deploying";
	const failed = done === "failed" || (shownError !== null && !submitting && draft.resourceId !== undefined);
	const formError = showForm && tried ? (error ?? blocker) : null;

	return (
		<>
			<SheetHeader>
				<span className="flex size-10 shrink-0 items-center justify-center rounded-xl border border-dashed border-line2 bg-bg3 text-fg2">
					<BoxIcon className="size-[18px]" />
				</span>
				<div className="flex min-w-0 flex-1 flex-col gap-[3px]">
					<div className="flex items-center gap-2">
						<SheetTitle className="text-xl">{draft.name}</SheetTitle>
						<Badge size="sm" tone={statusBadge.tone} className="h-5 px-[7px]">
							{statusBadge.label}
						</Badge>
					</div>
					<span className="truncate text-sm text-fg3" title={draft.image}>
						{draft.image}
					</span>
				</div>
			</SheetHeader>
			{showForm ? (
				<>
					<SheetBody>
						<DraftForm
							draft={draft}
							region={region}
							regions={regions}
							platformDomain={platformDomain}
							errors={errors}
							tried={tried}
							availability={availability}
							healthOpen={healthOpen}
							onHealthOpenChange={setHealthOpen}
							update={update}
						/>
					</SheetBody>
					<SheetFooter>
						{formError !== null && (
							<span className="flex items-center gap-1.5 text-sm text-bad-fg">
								<CircleAlertIcon className="size-[13px]" />
								{formError}
							</span>
						)}
						<Button
							size="xl"
							className={cn("shadow-[0_1px_2px_rgba(30,64,175,0.25)]", blocker !== null && "opacity-55")}
							onClick={() => {
								void deploy();
							}}
						>
							Deploy {draft.name}
						</Button>
						<Button
							variant="ghost"
							size="xs"
							className="self-center text-fg3 hover:bg-transparent hover:text-red"
							onClick={() => {
								remove();
								onClose();
							}}
						>
							Discard draft
						</Button>
					</SheetFooter>
				</>
			) : (
				<>
					<DraftProgress title={progTitle} elapsed={formatElapsed(endAt - startedAt)} steps={steps} />
					<div className="flex gap-2 border-t border-line px-6 pt-5 pb-6">
						{done === "live" && draft.resourceId !== undefined ? (
							<Button
								size="xl"
								className="flex-1"
								onClick={() => {
									const id = draft.resourceId;
									remove();
									if (id !== undefined) void navigate(resourcePath(orgId, workspaceId, id));
								}}
							>
								Open {draft.name}
							</Button>
						) : (
							<>
								{failed && draft.resourceId !== undefined && (
									<Button
										size="xl"
										className="flex-1"
										onClick={() => {
											const id = draft.resourceId;
											if (id === undefined) return;
											setError(null);
											setSubmitting(true);
											update({ deploymentId: undefined, deployedAt: Date.now() });
											startDeployment(id)
												.catch((err: unknown) => {
													setError(getErrorMessage(err, "Deployment failed"));
												})
												.finally(() => {
													setSubmitting(false);
												});
										}}
									>
										Retry deploy
									</Button>
								)}
								<Button
									variant="outline"
									size="xl"
									className="flex-1 font-normal"
									onClick={() => {
										if (draft.resourceId !== undefined && !submitting) remove();
										onClose();
									}}
								>
									Close
								</Button>
							</>
						)}
					</div>
				</>
			)}
		</>
	);
}
