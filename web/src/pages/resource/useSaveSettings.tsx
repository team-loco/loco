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
	HEALTH_KEYS,
	REGION_MAP_KEYS,
	domainsChange,
	failurePolicyChanged,
	failurePolicyFrom,
	healthRemoved,
	plainEnv,
	planSave,
	regionChanges,
	regionTargets,
	resolved,
	type Baseline,
	type SettingsDraft,
	variablesChanged,
} from "./settings-draft";

const SPEC_KEYS = ["image", "port", "healthEnabled", ...HEALTH_KEYS, "observability"] as const;

const DOMAIN_KEYS = ["isPublic", "subdomain", "appDomain"] as const;

const VARIABLE_KEYS = ["added", "env", "removedEnv"] as const;

function omit(draft: SettingsDraft, keys: readonly (keyof SettingsDraft)[]): SettingsDraft {
	return Object.fromEntries(Object.entries(draft).filter(([k]) => !keys.some((key) => key === k)));
}

export function useSaveSettings({
	resource,
	environmentId,
	regions,
	base,
	onDone,
}: {
	resource: Resource;
	environmentId: string;
	regions: RegionView[];
	base: Baseline;
	onDone: (next: SettingsDraft, notice: Notice) => void;
}) {
	const [saving, setSaving] = useState(false);
	const update = useMutation(updateResource);
	const addDomain = useMutation(createResourceDomain);
	const removeDomain = useMutation(deleteResourceDomain);
	const deploy = useMutation(createDeployment);
	const transport = useTransport();
	const queryClient = useQueryClient();

	// The API refuses a stale region map with FAILED_PRECONDITION; only a moved revision means someone else saved first.
	const revisionMoved = async (err: unknown) => {
		if (!(err instanceof ConnectError) || err.code !== Code.FailedPrecondition) return false;
		const latest = await callUnaryMethod(transport, getResource, {
			key: { case: "resourceId", value: resource.id },
		});
		return latest.resource?.revision !== resource.revision;
	};

	const save = async (draft: SettingsDraft) => {
		setSaving(true);
		const resourceId = resource.id;
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
				setSaving(false);
				onDone(draft, {
					tone: "bad",
					title: withCode`Couldn't save ${resource.name}`,
					message: getErrorMessage(err, "Failed to load its variables"),
				});
				return;
			}
		}
		const env = current === undefined ? undefined : plainEnv(current.env, draft);
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

		const effective = domainsSaved ? draft : omit(draft, DOMAIN_KEYS);
		const spec = resource.spec?.spec;
		const targets = regionTargets(spec?.case === "service" ? spec.value.regions : {}, effective, base);
		if (targets !== undefined) {
			try {
				await update.mutateAsync({ resourceId, regions: { targets }, revision: resource.revision });
				next = omit(next, REGION_MAP_KEYS);
			} catch (err) {
				if (await revisionMoved(err)) {
					setSaving(false);
					onDone(omit(next, REGION_MAP_KEYS), {
						tone: "warn",
						title: withCode`${resource.name} changed since you opened it`,
						message: "Showing its current regions and scaling. Make your region changes again.",
					});
					return;
				}
				failures.push(`Regions: ${getErrorMessage(err, "failed")}`);
			}
		}
		if (failurePolicyChanged(effective, base)) {
			await attempt(
				"Deployment behavior",
				FAILURE_POLICY_KEYS,
				async () =>
					await update.mutateAsync({ resourceId, failurePolicy: failurePolicyFrom(resolved(effective, base)) }),
			);
		}

		const deploys = planSave(regions, effective, base);
		const savedRegions: string[] = [];
		let deployFailed = false;
		for (const planned of deploys) {
			if (env !== undefined && planned.spec.spec.case === "service") planned.spec.spec.value.env = env;
			try {
				await deploy.mutateAsync({
					resourceId,
					regions: planned.regions,
					spec: planned.spec,
					environmentId,
					commit: planned.commit,
					removeHealthCheck: healthRemoved(effective),
				});
				savedRegions.push(...planned.regions);
			} catch (err) {
				deployFailed = true;
				failures.push(`${planned.regions.join(", ")}: ${getErrorMessage(err, "failed")}`);
			}
		}
		if (!deployFailed) next = omit(next, [...SPEC_KEYS, ...VARIABLE_KEYS]);
		if (current !== undefined) await refreshVariables(queryClient, resourceId, environmentId);

		setSaving(false);
		if (failures.length > 0) {
			onDone(next, {
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
			targets === undefined
				? { rolling: deploys.flatMap((d) => d.regions), stopping: [] }
				: regionChanges(effective, base);
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
		onDone(next, { tone: "info", title: withCode`Saved ${renamed ? name : resource.name}`, message });
	};

	return { save, saving };
}
