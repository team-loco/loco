import { useQuery } from "@connectrpc/connect-query";
import { ArrowLeftIcon } from "lucide-react";
import { useCallback, useRef, useState } from "react";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import { listPlatformDomains } from "@gen/loco/domain/v1/domain-DomainService_connectquery";
import { listRegions } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { DangerZone } from "@/components/design/DangerZone";
import { Notice } from "@/components/design/Notice";
import { SaveBar } from "@/components/design/SaveBar";
import { NEEDS_ADMIN } from "@/hooks/useMyScopes";
import { servedDomains } from "@/lib/new-resource";
import { isWorker } from "@/lib/resource-regions";
import { sizeOptions } from "@/lib/sizing";
import { cn } from "@/lib/utils";

import { MANAGED_NOTICE_ID, ManagedNotice } from "./ManagedNotice";
import { isDeployed, type Notice as NoticeState, type RegionView } from "./model";
import { NetworkingSection } from "./NetworkingSection";
import {
	changeNames,
	domainsChange,
	LIVE_ERROR_KEYS,
	planSave,
	regionMapChanged,
	resolved,
	validateDraft,
	type Baseline,
	type DraftErrors,
	type SettingsDraft,
} from "./settings-draft";
import {
	CommandSection,
	GeneralSection,
	HEALTH_SECTION_ID,
	HealthSection,
	ImageSection,
	RegionsSection,
	ScalingSection,
	SettingsBlock,
	SizeSection,
	ObservabilitySection,
	DeploymentSection,
} from "./SettingsSections";
import { useSaveSettings } from "./useSaveSettings";
import { VariablesSection } from "./VariablesSection";

export const SETTINGS_FOCUSES = ["settings", "scaling"] as const;
export type SettingsFocus = (typeof SETTINGS_FOCUSES)[number];

const READ_ONLY_ID = "settings-read-only";

const NAV = [
	{ id: "set-image", label: "Image" },
	{ id: "set-command", label: "Start command" },
	{ id: "set-scaling", label: "Scaling" },
	{ id: "set-size", label: "Size" },
	{ id: "set-env", label: "Variables" },
	{ id: "set-deployment", label: "Deployment behavior" },
	{ id: "set-observability", label: "Observability" },
	{ id: "set-net", label: "Networking" },
	{ id: HEALTH_SECTION_ID, label: "Health check" },
	{ id: "set-regions", label: "Regions" },
	{ id: "set-general", label: "General" },
	{ id: "set-danger", label: "Danger zone" },
];

function scrollToSection(node: HTMLElement | null) {
	node?.scrollIntoView({ block: "start", behavior: "instant" });
}

function scrollToHash(node: HTMLElement | null) {
	const id = window.location.hash.slice(1);
	if (node !== null && id !== "")
		node.querySelector(`#${CSS.escape(id)}`)?.scrollIntoView({ block: "start", behavior: "instant" });
}

const SPY_VIEWPORT_FRACTION = 0.3;
const USER_INPUTS = ["wheel", "touchmove", "keydown", "pointerdown"] as const;

// The last section whose top is within the upper part of the viewport.
function sectionInView(): string | undefined {
	const ids = NAV.map((n) => n.id).filter((id) => document.getElementById(id) !== null);
	let found = ids[0];
	for (const id of ids) {
		const top = document.getElementById(id)?.getBoundingClientRect().top;
		if (top !== undefined && top <= window.innerHeight * SPY_VIEWPORT_FRACTION) found = id;
	}
	return found;
}

// A smooth scroll moves the page under the pointer, so a click can land on a control the user did not aim at.
function jumpToSection() {
	const root = document.documentElement;
	root.style.scrollBehavior = "auto";
	setTimeout(() => {
		root.style.scrollBehavior = "";
	}, 0);
}

export function SettingsView({
	resource,
	environmentId,
	regions,
	draft,
	base,
	focus,
	canWrite,
	canDelete,
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
	canDelete: boolean;
	canReadWorkspace: boolean;
	onDraft: (draft: SettingsDraft) => void;
	onBack: () => void;
	onDelete: () => void;
	onSaved: (notice: NoticeState) => void;
}) {
	const [current, setCurrent] = useState(() => {
		const linked = NAV.find((n) => `#${n.id}` === window.location.hash);
		return focus === "scaling" ? "set-scaling" : (linked?.id ?? "set-image");
	});
	// A clicked or linked section stays highlighted until the user scrolls again.
	const pinnedRef = useRef(focus === "scaling" || window.location.hash !== "");
	const sectionsRef = useCallback((node: HTMLElement | null) => {
		scrollToHash(node);
		if (node === null) return;
		const follow = () => {
			if (pinnedRef.current) return;
			const id = sectionInView();
			if (id !== undefined) setCurrent(id);
		};
		const release = () => {
			pinnedRef.current = false;
		};
		document.addEventListener("scroll", follow, { capture: true, passive: true });
		for (const type of USER_INPUTS) document.addEventListener(type, release, { passive: true });
		const resize = new ResizeObserver(follow);
		resize.observe(node);
		return () => {
			resize.disconnect();
			document.removeEventListener("scroll", follow, { capture: true });
			for (const type of USER_INPUTS) document.removeEventListener(type, release);
		};
	}, []);
	const [tried, setTried] = useState(false);
	const [confirmingInternal, setConfirmingInternal] = useState(false);
	const regionsQuery = useQuery(listRegions, { environmentId }, { enabled: canReadWorkspace && environmentId !== "" });
	const domainsQuery = useQuery(
		listPlatformDomains,
		{ environmentId },
		{ enabled: canReadWorkspace && environmentId !== "" },
	);
	const configQuery = useQuery(getConfig, {});
	const sizes = sizeOptions(configQuery.data?.sizeLimits);
	const managed = resource.partial !== undefined;
	const worker = isWorker(
		resource,
		regions.filter(isDeployed).flatMap((r) => (r.current === undefined ? [] : [r.current])),
	);
	const locked = managed || !canWrite;
	const describedBy = managed ? MANAGED_NOTICE_ID : canWrite ? undefined : READ_ONLY_ID;
	const want = resolved(draft, base);
	const onRegions = Object.keys(want.regions).filter((r) => want.regions[r] === true);
	const served = servedDomains(domainsQuery.data?.platformDomains ?? [], onRegions);
	const errors = validateDraft(draft, base, regions, served, sizes.replicas?.max ?? Number.POSITIVE_INFINITY);
	const errorCount = Object.keys(errors).length;
	const changes = changeNames(draft, base);
	const count = changes.length;
	const redeploys = regionMapChanged(draft, base) || planSave(regions, draft, base).length > 0;
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
	const shownErrors: DraftErrors = tried
		? errors
		: Object.fromEntries(LIVE_ERROR_KEYS.map((key) => [key, errors[key]]));
	const props = { draft, base, errors: shownErrors, locked, describedBy, onChange: change };

	return (
		<div className="flex flex-col gap-5">
			<div className="flex items-center gap-3 border-b border-line pb-3.5">
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
					{NAV.filter((n) => !worker || n.id !== HEALTH_SECTION_ID).map((n) => (
						<a
							key={n.id}
							href={`#${n.id}`}
							aria-current={current === n.id ? "true" : undefined}
							onClick={() => {
								jumpToSection();
								pinnedRef.current = true;
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
				<div ref={sectionsRef} className="flex min-w-0 flex-col">
					<ImageSection {...props} />
					<CommandSection {...props} />
					<ScalingSection
						{...props}
						sizes={sizes}
						regions={regions}
						sectionRef={focus === "scaling" ? scrollToSection : undefined}
					/>
					<SizeSection {...props} sizes={sizes} />
					<VariablesSection
						resourceId={resource.id}
						resourceName={resource.name}
						managed={managed}
						deployed={regions.some(isDeployed)}
						workspaceId={resource.workspaceId}
						environmentId={environmentId}
						draft={draft}
						canWrite={canWrite}
						canReadWorkspace={canReadWorkspace}
						onChange={change}
					/>
					<DeploymentSection {...props} />
					<ObservabilitySection {...props} />
					<NetworkingSection
						{...props}
						resourceId={resource.id}
						domains={resource.domains}
						platformDomains={domainsQuery.data?.platformDomains ?? []}
						regions={regionsQuery.data?.regions ?? []}
						served={served}
						worker={worker}
						onDomainsChanged={() => {
							onSaved({
								tone: "info",
								title: "Domain updated",
								message: "It takes effect on the next deployment.",
							});
						}}
					/>
					{!worker && <HealthSection {...props} />}
					<RegionsSection {...props} errors={errors} available={regionsQuery.data?.regions ?? []} />
					<GeneralSection {...props} canWrite={canWrite} readOnlyId={READ_ONLY_ID} />
					<SettingsBlock id="set-danger" title="Danger zone">
						<DangerZone
							title={`Delete ${resource.displayName}`}
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
							blockedReason={canDelete ? null : NEEDS_ADMIN}
							onAction={onDelete}
						/>
					</SettingsBlock>
					{count > 0 && (
						<SaveBar
							count={count}
							names={changes}
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
								if (errorCount > 0) return;
								if (domainsChange(draft, base) === "remove") setConfirmingInternal(true);
								else void save(draft);
							}}
						/>
					)}
					<ConfirmDialog
						open={confirmingInternal}
						title={`Make ${resource.name} internal?`}
						description="Saving removes every domain below and redeploys, so the resource only answers inside the workspace."
						confirmLabel="Remove domains and save"
						onConfirm={() => {
							setConfirmingInternal(false);
							void save(draft);
						}}
						onClose={() => {
							setConfirmingInternal(false);
						}}
					>
						<ul className="m-0 flex list-none flex-col gap-1 p-0 font-mono text-[13px]">
							{resource.domains.map((d) => (
								<li key={d.domain}>{d.domain}</li>
							))}
						</ul>
					</ConfirmDialog>
				</div>
			</div>
		</div>
	);
}
