import { Code, ConnectError } from "@connectrpc/connect";
import { InlineCodeList, withCode } from "@/components/design/InlineCode";
import { callUnaryMethod, useMutation, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { createDeployment } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { createResourceDomain, deleteResourceDomain } from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import { DomainType } from "@gen/loco/domain/v1/domain_pb";
import {
	getResource,
	getResourceEnv,
	updateResource,
} from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { GetResourceEnvResponse, Resource } from "@gen/loco/resource/v1/resource_pb";

import { getErrorMessage } from "@/lib/error-handler";

import { refreshVariables } from "./variables";

import type { Notice, RegionView } from "./model";
import {
	FAILURE_POLICY_KEYS,
	REGION_MAP_KEYS,
	domainsChange,
	effectiveDraft,
	failurePolicyChanged,
	failurePolicyFrom,
	plainEnv,
	planSave,
	regionChanges,
	regionTargets,
	resolved,
	type Baseline,
	type DraftErrors,
	type SettingsDraft,
	variablesChanged,
} from "./settings-draft";

const SPEC_KEYS = ["image", "port", "health", "observability", "command", "args"] as const;

const DOMAIN_KEYS = ["isPublic", "subdomain", "appDomain"] as const;

const VARIABLE_KEYS = ["added", "env", "removedEnv"] as const;

function omit(draft: SettingsDraft, keys: readonly (keyof SettingsDraft)[]): SettingsDraft {
	return Object.fromEntries(Object.entries(draft).filter(([k]) => !keys.some((key) => key === k)));
}

/** What a save works from, read when it starts. */
export interface SaveRequest {
	resource: Resource;
	environmentId: string;
	regions: RegionView[];
	/** What runs now; the resource updates and domains run only where it differs from the draft. */
	base: Baseline;
	draft: SettingsDraft;
	/** Validates the draft against the variables and plan this save uses. */
	validate: (env: Record<string, string>, hasDeployments: boolean) => DraftErrors;
}

/**
 * Saves resource settings. The page that owns the draft holds it, so the save and its lock outlive
 * the settings tab. `onDone` gets the draft to keep, empty once everything saved.
 */
export function useSaveSettings(onDone: (next: SettingsDraft, notice: Notice) => Promise<void>) {
	const [saving, setSaving] = useState(false);
	const update = useMutation(updateResource);
	const addDomain = useMutation(createResourceDomain);
	const removeDomain = useMutation(deleteResourceDomain);
	const deploy = useMutation(createDeployment);
	const transport = useTransport();
	const queryClient = useQueryClient();

	// The API refuses a stale region map with FAILED_PRECONDITION; only a moved revision means someone else saved first.
	const revisionMoved = async (err: unknown, resource: Resource) => {
		if (!(err instanceof ConnectError) || err.code !== Code.FailedPrecondition) return false;
		try {
			const latest = await callUnaryMethod(transport, getResource, {
				key: { case: "resourceId", value: resource.id },
			});
			return latest.resource?.revision !== resource.revision;
		} catch {
			return false;
		}
	};

	const saveAll = async ({ resource, environmentId, regions, base, draft: raw, validate }: SaveRequest) => {
		const resourceId = resource.id;
		const draft = effectiveDraft(raw, base);
		const failures: string[] = [];
		let next = draft;
		const attempt = async (label: string, keys: readonly (keyof SettingsDraft)[], run: () => Promise<unknown>) => {
			try {
				await run();
				next = omit(next, keys);
				return true;
			} catch (err) {
				failures.push(`${label}: ${getErrorMessage(err, "failed")}`);
				return false;
			}
		};

		let current: GetResourceEnvResponse | undefined;
		if (variablesChanged(draft)) {
			try {
				current = await callUnaryMethod(transport, getResourceEnv, { resourceId, environmentId });
			} catch (err) {
				await onDone(raw, {
					tone: "bad",
					title: withCode`Couldn't save ${resource.name}`,
					message: getErrorMessage(err, "Failed to load its variables"),
				});
				return;
			}
		}
		// The plan this save sends is the one validated here, before anything changes.
		const deploys = planSave(regions, draft, base, plainEnv(current?.env ?? {}, draft));
		const [problem] = Object.values(validate(current?.env ?? {}, deploys.length > 0));
		if (problem !== undefined) {
			await onDone(raw, { tone: "bad", title: withCode`Couldn't save ${resource.name}`, message: problem });
			return;
		}

		const name = draft.name?.trim();
		const renamed = name !== undefined && name !== resource.name;
		const description = draft.description?.trim();
		const described = description !== undefined && description !== (resource.description ?? "");
		if (renamed || described) {
			await attempt(
				"General",
				["name", "description"],
				async () =>
					await update.mutateAsync({
						resourceId,
						...(renamed ? { name } : {}),
						...(described ? { description } : {}),
					}),
			);
		}

		const domainChange = domainsChange(draft, base);
		let domainsSaved = true;
		if (domainChange === "add") {
			domainsSaved = await attempt(
				"Domain",
				DOMAIN_KEYS,
				async () =>
					await addDomain.mutateAsync({
						resourceId,
						domain: {
							domainSource: DomainType.APP,
							subdomain: draft.subdomain ?? "",
							appDomain: draft.appDomain ?? "",
						},
					}),
			);
		}
		if (domainChange === "remove") {
			const ordered = resource.domains.toSorted((a, b) => Number(a.isPrimary) - Number(b.isPrimary));
			domainsSaved = await attempt("Domains", DOMAIN_KEYS, async () => {
				for (const d of ordered) await removeDomain.mutateAsync({ domainId: d.id });
			});
		}

		const spec = resource.spec?.spec;
		const targets = regionTargets(spec?.case === "service" ? spec.value.regions : {}, draft, base);
		if (targets !== undefined) {
			try {
				await update.mutateAsync({ resourceId, regions: { targets }, revision: resource.revision });
				next = omit(next, REGION_MAP_KEYS);
			} catch (err) {
				if (await revisionMoved(err, resource)) {
					await onDone(omit(next, REGION_MAP_KEYS), {
						tone: "warn",
						title: withCode`${resource.name} changed since you opened it`,
						message: "Showing its current regions and scaling. Make your region changes again.",
					});
					return;
				}
				failures.push(`Regions: ${getErrorMessage(err, "failed")}`);
			}
		}
		if (failurePolicyChanged(draft, base)) {
			await attempt(
				"Deployment behavior",
				FAILURE_POLICY_KEYS,
				async () => await update.mutateAsync({ resourceId, failurePolicy: failurePolicyFrom(resolved(draft, base)) }),
			);
		}

		// Without the domain change, only the deployments the other settings change are sent.
		const sent = domainsSaved
			? deploys
			: planSave(regions, omit(draft, DOMAIN_KEYS), base, plainEnv(current?.env ?? {}, draft));
		const savedRegions: string[] = [];
		for (const planned of sent) {
			try {
				await deploy.mutateAsync({
					resourceId,
					regions: planned.regions,
					spec: planned.spec,
					environmentId,
					commit: planned.commit,
					removeHealthCheck: draft.health?.form.enabled === false,
				});
				savedRegions.push(...planned.regions);
			} catch (err) {
				failures.push(`${planned.regions.join(", ")}: ${getErrorMessage(err, "failed")}`);
			}
		}
		// Once any deployment saved, the deployment settings clear: the reload shows what each region runs.
		if (sent.length === 0 || savedRegions.length > 0) next = omit(next, [...SPEC_KEYS, ...VARIABLE_KEYS]);
		if (current !== undefined) await refreshVariables(queryClient, resourceId, environmentId);

		if (failures.length > 0) {
			await onDone(next, {
				tone: "bad",
				title: withCode`Some changes to ${resource.name} were not saved`,
				message: (
					<>
						{failures.join("\n")}
						{savedRegions.length > 0 && (
							<>
								{"\n"}Saved changes in <InlineCodeList names={savedRegions} />.
							</>
						)}
					</>
				),
			});
			return;
		}
		const { rolling, stopping } =
			targets === undefined ? { rolling: sent.flatMap((d) => d.regions), stopping: [] } : regionChanges(draft, base);
		const message =
			rolling.length + stopping.length === 0 ? (
				"No redeploy needed."
			) : (
				<>
					{rolling.length > 0 && (
						<>
							Rolling out in <InlineCodeList names={rolling} />.
						</>
					)}
					{rolling.length > 0 && stopping.length > 0 && " "}
					{stopping.length > 0 && (
						<>
							Stopping in <InlineCodeList names={stopping} />.
						</>
					)}
				</>
			);
		await onDone(next, { tone: "info", title: withCode`Saved ${renamed ? name : resource.name}`, message });
	};

	// Saving lasts until the page has reloaded what runs, so the next edit starts from it.
	const save = async (request: SaveRequest) => {
		setSaving(true);
		try {
			await saveAll(request);
		} finally {
			setSaving(false);
		}
	};

	return { save, saving };
}
