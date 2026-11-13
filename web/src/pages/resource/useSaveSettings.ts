import { Code, ConnectError } from "@connectrpc/connect";
import { callUnaryMethod, useMutation, useTransport } from "@connectrpc/connect-query";
import { useState } from "react";
import { createDeployment } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { createResourceDomain, deleteResourceDomain } from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import { DomainType } from "@gen/loco/domain/v1/domain_pb";
import { getResource, updateResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";
import { deleteSecrets, setSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";

import { getErrorMessage } from "@/lib/error-handler";

import type { Notice, RegionView } from "./model";
import {
	REGION_MAP_KEYS,
	domainsChange,
	planSave,
	regionChanges,
	regionTargets,
	type Baseline,
	type SettingsDraft,
} from "./settings-draft";

const SPEC_KEYS = ["image", "port", "healthPath", "healthInterval", "healthTimeout", "healthFailures"] as const;

const DOMAIN_KEYS = ["isPublic", "subdomain", "platformDomain"] as const;

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
	const writeSecrets = useMutation(setSecrets);
	const removeSecrets = useMutation(deleteSecrets);
	const addDomain = useMutation(createResourceDomain);
	const removeDomain = useMutation(deleteResourceDomain);
	const deploy = useMutation(createDeployment);
	const transport = useTransport();

	// The API refuses a stale region map with FAILED_PRECONDITION, as it does a rollout in
	// progress; only a moved revision means someone else saved first.
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

		const secrets = draft.secrets ?? {};
		if (Object.keys(secrets).length > 0) {
			await attempt(
				"Secrets",
				["secrets"],
				async () => await writeSecrets.mutateAsync({ resourceId, environmentId, values: secrets }),
			);
		}
		const removed = draft.removedSecrets ?? [];
		if (removed.length > 0) {
			await attempt(
				"Removing secrets",
				["removedSecrets"],
				async () => await removeSecrets.mutateAsync({ resourceId, environmentId, names: removed }),
			);
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
							domainSource: DomainType.PLATFORM_PROVIDED,
							subdomain: draft.subdomain ?? "",
							platformDomain: draft.platformDomain ?? "",
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
						title: `${resource.name} changed since you opened it`,
						message: "Showing its current regions and scaling. Make your region changes again.",
					});
					return;
				}
				failures.push(`Regions: ${getErrorMessage(err, "failed")}`);
			}
		}
		const deploys = planSave(regions, effective, base);
		const savedRegions: string[] = [];
		for (const d of deploys) {
			const ok = await attempt(
				d.region,
				[],
				async () =>
					await deploy.mutateAsync({ resourceId, region: d.region, spec: d.spec, environmentId, commit: d.commit }),
			);
			if (ok) savedRegions.push(d.region);
		}
		if (savedRegions.length === deploys.length) next = omit(next, SPEC_KEYS);

		setSaving(false);
		if (failures.length > 0) {
			onDone(next, {
				tone: "bad",
				title: `Some changes to ${resource.name} were not saved`,
				message: [
					...failures,
					...(savedRegions.length > 0 ? [`Saved changes in ${savedRegions.join(", ")}.`] : []),
				].join(" · "),
			});
			return;
		}
		const parts = [];
		const { rolling, stopping } =
			targets === undefined ? { rolling: deploys.map((d) => d.region), stopping: [] } : regionChanges(effective, base);
		if (rolling.length > 0) parts.push(`Rolling out in ${rolling.join(", ")}.`);
		if (stopping.length > 0) parts.push(`Stopping in ${stopping.join(", ")}.`);
		const message = parts.length === 0 ? "No redeploy needed." : parts.join(" ");
		onDone(next, { tone: "info", title: `Saved ${name ?? resource.name}`, message });
	};

	return { save, saving };
}
