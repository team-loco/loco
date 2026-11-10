import { useQuery } from "@connectrpc/connect-query";
import { ArrowLeftIcon } from "lucide-react";
import { useState } from "react";
import { listPlatformDomains } from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import { listRegions } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { DangerZone } from "@/components/design/DangerZone";
import { Notice } from "@/components/design/Notice";
import { SaveBar } from "@/components/design/SaveBar";
import { servedDomains } from "@/lib/new-resource";
import { configuredRegions } from "@/lib/resource-regions";
import { cn } from "@/lib/utils";

import { MANAGED_NOTICE_ID, ManagedNotice } from "./ManagedNotice";
import type { Notice as NoticeState, RegionView } from "./model";
import { NetworkingSection } from "./NetworkingSection";
import { changeCount, planSave, resolved, validateDraft, type Baseline, type SettingsDraft } from "./settings-draft";
import {
	GeneralSection,
	HealthSection,
	ImageSection,
	RegionsSection,
	ScalingSection,
	SettingsBlock,
	SizeSection,
} from "./SettingsSections";
import { useSaveSettings } from "./useSaveSettings";
import { VariablesSection } from "./VariablesSection";

export type SettingsFocus = "settings" | "scaling";

const READ_ONLY_ID = "settings-read-only";

const NAV = [
	{ id: "set-image", label: "Image" },
	{ id: "set-scaling", label: "Scaling" },
	{ id: "set-size", label: "Size" },
	{ id: "set-env", label: "Variables" },
	{ id: "set-net", label: "Networking" },
	{ id: "set-health", label: "Health check" },
	{ id: "set-regions", label: "Regions" },
	{ id: "set-general", label: "General" },
	{ id: "set-danger", label: "Danger zone" },
];

function scrollToSection(node: HTMLElement | null) {
	node?.scrollIntoView({ block: "start" });
}

export function SettingsView({
	resource,
	environmentId,
	regions,
	draft,
	base,
	focus,
	canWrite,
	canReadWorkspace,
	onDraft,
	onBack,
	onDelete,
	onSaved,
}: {
	resource: Resource;
	environmentId: string;
	regions: RegionView[];
	draft: SettingsDraft;
	base: Baseline;
	focus: SettingsFocus;
	canWrite: boolean;
	canReadWorkspace: boolean;
	onDraft: (draft: SettingsDraft) => void;
	onBack: () => void;
	onDelete: () => void;
	onSaved: (notice: NoticeState) => void;
}) {
	const [current, setCurrent] = useState(focus === "scaling" ? "set-scaling" : "set-image");
	const [tried, setTried] = useState(false);
	const regionsQuery = useQuery(listRegions, { environmentId }, { enabled: environmentId !== "" });
	const domainsQuery = useQuery(listPlatformDomains, { environmentId }, { enabled: environmentId !== "" });
	const managed = resource.partial !== undefined;
	const locked = managed || !canWrite;
	const describedBy = managed ? MANAGED_NOTICE_ID : canWrite ? undefined : READ_ONLY_ID;
	const want = resolved(draft, base);
	const onRegions = Object.keys(want.regions).filter((r) => want.regions[r] === true);
	const served = servedDomains(domainsQuery.data?.platformDomains ?? [], onRegions);
	const errors = validateDraft(draft, base, served);
	const errorCount = Object.keys(errors).length;
	const count = changeCount(draft, base);
	const plan = planSave(regions, draft, base);
	const redeploys = plan.deploy.length + plan.retire.length > 0;
	const { save, saving } = useSaveSettings({
		resource,
		environmentId,
		regions,
		base,
		onDone: (next, notice) => {
			onDraft(next);
			setTried(false);
			onSaved(notice);
		},
	});
	const change = (patch: Partial<SettingsDraft>) => {
		onDraft({ ...draft, ...patch });
	};
	const shownErrors = tried ? errors : {};
	const props = { draft, base, errors: shownErrors, locked, describedBy, onChange: change };

	return (
		<div className="flex flex-col gap-5">
			<div className="flex items-center gap-3">
				<Button variant="outline" size="sm" onClick={onBack}>
					<ArrowLeftIcon />
					Overview
				</Button>
				<h2 className="m-0 text-[16px] font-medium tracking-[-0.01em]">Settings</h2>
			</div>
			{managed && <ManagedNotice />}
			{!canWrite && (
				<Notice id={READ_ONLY_ID}>
					You can view these settings. Changing them needs write access to this resource.
				</Notice>
			)}
			<div className="grid grid-cols-1 items-start gap-8 md:grid-cols-[170px_minmax(0,1fr)]">
				<nav aria-label="Settings sections" className="flex flex-row flex-wrap gap-0.5 md:sticky md:top-4 md:flex-col">
					{NAV.map((n) => (
						<a
							key={n.id}
							href={`#${n.id}`}
							aria-current={current === n.id ? "true" : undefined}
							onClick={() => {
								setCurrent(n.id);
							}}
							className={cn(
								"flex h-8 items-center rounded-md px-2.5 text-[13.5px] text-fg2 hover:bg-panel aria-[current=true]:bg-panel aria-[current=true]:font-medium aria-[current=true]:text-foreground",
							)}
						>
							{n.label}
						</a>
					))}
				</nav>
				<div className="flex min-w-0 flex-col">
					<ImageSection {...props} />
					<ScalingSection {...props} regions={regions} sectionRef={focus === "scaling" ? scrollToSection : undefined} />
					<SizeSection {...props} />
					<VariablesSection
						resourceId={resource.id}
						workspaceId={resource.workspaceId}
						environmentId={environmentId}
						draft={draft}
						canWrite={canWrite}
						canReadWorkspace={canReadWorkspace}
						onChange={change}
					/>
					<NetworkingSection
						{...props}
						resourceId={resource.id}
						domains={resource.domains}
						platformDomains={domainsQuery.data?.platformDomains ?? []}
						served={served}
						onDomainsChanged={() => {
							onSaved({ tone: "info", title: "Domain updated", message: "It takes effect on the next deployment." });
						}}
					/>
					<HealthSection {...props} />
					<RegionsSection
						{...props}
						errors={errors}
						available={regionsQuery.data?.regions ?? []}
						inSpec={configuredRegions(resource)}
					/>
					<GeneralSection {...props} description={resource.description ?? ""} />
					<SettingsBlock id="set-danger" title="Danger zone">
						<DangerZone
							title={`Delete ${resource.name}`}
							description={
								managed ? (
									<>
										Deleting here causes drift with <code className="font-mono">loco.yaml</code>.
									</>
								) : (
									"Removes its deployments in every region and its domains."
								)
							}
							actionLabel="Delete resource"
							blockedReason={canWrite ? null : "Needs write access to this resource"}
							onAction={onDelete}
						/>
					</SettingsBlock>
					{count > 0 && (
						<SaveBar
							count={count}
							saveLabel={redeploys ? "Save & redeploy" : "Save"}
							saving={saving}
							disabledReason={
								tried && errorCount > 0
									? `Fix ${errorCount.toString()} ${errorCount === 1 ? "field" : "fields"} first`
									: null
							}
							onDiscard={() => {
								onDraft({});
								setTried(false);
							}}
							onSave={() => {
								setTried(true);
								if (errorCount === 0) void save(draft);
							}}
						/>
					)}
				</div>
			</div>
		</div>
	);
}
