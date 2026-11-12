import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";
import { createDeployment, deleteDeployment } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { createResourceDomain, deleteResourceDomain } from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import { DomainType } from "@gen/loco/domain/v1/domain_pb";
import { updateResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";
import { deleteSecrets, setSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";

import { getErrorMessage } from "@/lib/error-handler";

import type { Notice, RegionView } from "./model";
import { domainsChange, planSave, type Baseline, type SettingsDraft } from "./settings-draft";

const SPEC_KEYS = [
	"image",
	"replicas",
	"cpu",
	"memory",
	"port",
	"healthPath",
	"healthInterval",
	"healthTimeout",
	"healthFailures",
	"regions",
] as const;

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
	const retire = useMutation(deleteDeployment);

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

		const plan = planSave(regions, domainsSaved ? draft : omit(draft, DOMAIN_KEYS), base);
		let specSaved = true;
		for (const r of plan.retire) {
			const ok = await attempt(r.region, [], async () => await retire.mutateAsync({ deploymentId: r.deploymentId }));
			specSaved &&= ok;
		}
		for (const d of plan.deploy) {
			const ok = await attempt(
				d.region,
				[],
				async () =>
					await deploy.mutateAsync({ resourceId, region: d.region, spec: d.spec, environmentId, commit: d.commit }),
			);
			specSaved &&= ok;
		}
		if (specSaved) next = omit(next, SPEC_KEYS);

		setSaving(false);
		if (failures.length > 0) {
			onDone(next, {
				tone: "bad",
				title: `Some changes to ${resource.name} were not saved`,
				message: failures.join(" · "),
			});
			return;
		}
		const parts = [];
		if (plan.deploy.length > 0) parts.push(`Rolling out in ${plan.deploy.map((d) => d.region).join(", ")}.`);
		if (plan.retire.length > 0) parts.push(`Stopping in ${plan.retire.map((r) => r.region).join(", ")}.`);
		const message = parts.length === 0 ? "No redeploy needed." : parts.join(" ");
		onDone(next, { tone: "info", title: `Saved ${name ?? resource.name}`, message });
	};

	return { save, saving };
}
