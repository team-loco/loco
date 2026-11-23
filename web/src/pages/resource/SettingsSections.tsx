import type { ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { getResource } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { useQuery } from "@connectrpc/connect-query";
import { getConfig } from "@gen/loco/config/v1/config-ConfigService_connectquery";
import type { RegionInfo } from "@gen/loco/resource/v1/resource_pb";

import { RegionCard, RegionCards } from "@/components/design/RegionCards";
import { CommandFields } from "@/components/design/CommandFields";
import { ErrorState } from "@/components/design/ErrorState";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { SegmentedChoice } from "@/components/design/SegmentedChoice";
import { SliderValue, StepSlider, stepLabel } from "@/components/design/StepSlider";
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from "@/components/design/InputGroup";
import { AutoscalingSwitch, ScalingFields } from "@/components/design/ScalingFields";
import { SoonTag } from "@/components/design/SoonTag";
import { Switch } from "@/components/design/Switch";
import { joinImage, parseImage, splitImage } from "@/lib/image";
import type { Scaling } from "@/lib/new-resource";
import { cpuWith, memoryWith, type SizeOptions } from "@/lib/sizing";

import {
	RESTART_CHOICES,
	scalingDiffers,
	type Baseline,
	type DraftErrors,
	type RestartChoice,
	type SettingsDraft,
} from "./settings-draft";

export const HEALTH_SECTION_ID = "set-health";

export interface SectionProps {
	draft: SettingsDraft;
	base: Baseline;
	errors: DraftErrors;
	locked: boolean;
	describedBy: string | undefined;
	onChange: (patch: Partial<SettingsDraft>) => void;
}

const grid2 = "grid grid-cols-[repeat(auto-fit,minmax(min(240px,100%),1fr))] gap-x-[18px] gap-y-3.5";

export function SettingsBlock({
	id,
	title,
	hint,
	actions,
	sectionRef,
	children,
}: {
	id: string;
	title: ReactNode;
	hint?: ReactNode;
	actions?: ReactNode;
	sectionRef?: ((node: HTMLElement | null) => void) | undefined;
	children: ReactNode;
}) {
	return (
		<section
			id={id}
			ref={sectionRef}
			aria-labelledby={`${id}-title`}
			className="flex scroll-mt-20 flex-col gap-4 border-b border-line py-[22px] first:pt-0 last:border-b-0"
		>
			<div className="flex flex-wrap items-center justify-between gap-3">
				<div className="flex flex-col gap-1">
					<h3 id={`${id}-title`} className="m-0 flex items-center gap-2 text-[15px] font-medium tracking-[-0.01em]">
						{title}
					</h3>
					{hint !== undefined && <p className="m-0 text-[13px] text-fg3">{hint}</p>}
				</div>
				{actions}
			</div>
			{children}
		</section>
	);
}

export function ImageSection({ draft, base, errors, locked, describedBy, onChange }: SectionProps) {
	const pinned = parseImage(base.image);
	const { repository: path, reference: version } =
		draft.image === undefined && pinned.tag !== ""
			? { repository: pinned.path, reference: pinned.tag }
			: splitImage(draft.image ?? base.image);
	const digest = draft.image === undefined && pinned.tag !== "" ? pinned.digest : "";
	const set = (repository: string, reference: string) => {
		onChange({ image: joinImage(repository, reference) });
	};
	return (
		<SettingsBlock
			id="set-image"
			title="Image"
			hint="Public images only. Loco pins the image by digest when it deploys."
		>
			<div className={grid2}>
				<Field label="Repository" error={errors.image}>
					<Input
						className="font-mono"
						value={path}
						disabled={locked}
						aria-describedby={describedBy}
						aria-invalid={errors.image !== undefined}
						spellCheck={false}
						onChange={(e) => {
							set(e.target.value, version);
						}}
					/>
				</Field>
				<Field
					label="Tag or digest"
					hint={digest === "" ? undefined : <span className="break-all font-mono">Pinned to {digest}</span>}
				>
					<Input
						className="font-mono"
						value={version}
						placeholder="latest"
						disabled={locked}
						aria-describedby={describedBy}
						spellCheck={false}
						onChange={(e) => {
							set(path, e.target.value);
						}}
					/>
				</Field>
			</div>
		</SettingsBlock>
	);
}

export function CommandSection({ draft, base, locked, describedBy, onChange }: SectionProps) {
	return (
		<SettingsBlock
			id="set-command"
			title="Start command"
			hint="How the container starts. Loco passes each line to the program as one entry, without a shell."
		>
			<CommandFields
				value={{ command: draft.command ?? base.command, args: draft.args ?? base.args }}
				disabled={locked}
				describedBy={describedBy}
				onChange={onChange}
			/>
		</SettingsBlock>
	);
}

export function ScalingSection({
	draft,
	base,
	errors,
	locked,
	onChange,
	sizes,
	sectionRef,
}: SectionProps & {
	sizes: SizeOptions;
	sectionRef: ((node: HTMLElement | null) => void) | undefined;
}) {
	const names = Object.keys(base.scaling);
	const first = names[0];
	const value = first === undefined ? undefined : (draft.scaling?.[first] ?? base.scaling[first]);
	const setAll = (scaling: Scaling) => {
		onChange({ scaling: Object.fromEntries(names.map((name) => [name, scaling])) });
	};
	const error = names.map((name) => errors[`scaling.${name}`]).find((e) => e !== undefined) ?? null;
	return (
		<SettingsBlock
			id="set-scaling"
			sectionRef={sectionRef}
			title="Scaling"
			hint="Applies to every region this resource runs in."
			actions={
				value !== undefined && (
					<AutoscalingSwitch value={value} replicaRange={sizes.replicas} disabled={locked} onChange={setAll} />
				)
			}
		>
			{value === undefined ? (
				<span className="text-fg3">Scaling is available once the resource is deployed.</span>
			) : (
				<ScalingFields value={value} replicaRange={sizes.replicas} disabled={locked} error={error} onChange={setAll} />
			)}
			{scalingDiffers(base) && (
				<span className="text-[12.5px] text-fg3">
					Regions scale differently now. Saving applies these values to every region.
				</span>
			)}
		</SettingsBlock>
	);
}

export function SizeSection({
	draft,
	base,
	locked,
	describedBy,
	onChange,
	sizes,
}: SectionProps & { sizes: SizeOptions }) {
	const cpuOptions = cpuWith(sizes.cpu, draft.cpu ?? base.cpu);
	const memoryOptions = memoryWith(sizes.memory, draft.memory ?? base.memory);
	const scalings = Object.values({ ...base.scaling, ...draft.scaling });
	const low = Math.min(...scalings.map((s) => s.replicas));
	const high = Math.max(...scalings.map((s) => s.maxReplicas));
	const replicas = low === high ? low.toString() : `${low.toString()}–${high.toString()}`;
	return (
		<SettingsBlock id="set-size" title="Size per replica" hint="Requests for each replica, applied in every region.">
			<div className="flex max-w-[520px] flex-col gap-3.5">
				<Field label="CPU" aside={<SliderValue text={stepLabel(cpuOptions, draft.cpu ?? base.cpu)} />}>
					<StepSlider
						label="CPU"
						tickLabel={(option) => option.label.replace(" vCPU", "")}
						options={cpuOptions}
						value={draft.cpu ?? base.cpu}
						disabled={locked}
						describedBy={describedBy}
						onChange={(cpu) => {
							onChange({ cpu });
						}}
					/>
				</Field>
				<Field label="Memory" aside={<SliderValue text={stepLabel(memoryOptions, draft.memory ?? base.memory)} />}>
					<StepSlider
						label="Memory"
						options={memoryOptions}
						value={draft.memory ?? base.memory}
						disabled={locked}
						describedBy={describedBy}
						onChange={(memory) => {
							onChange({ memory });
						}}
					/>
				</Field>
				<span className="flex flex-wrap items-center gap-1.5 text-[12.5px] text-fg3">
					{scalings.length > 0 && (
						<span className="font-mono text-[12px]">
							{`${replicas} × (${stepLabel(cpuOptions, draft.cpu ?? base.cpu)}, ${stepLabel(memoryOptions, draft.memory ?? base.memory)})`}
						</span>
					)}
					<span>Cost per replica</span>
					<SoonTag />
				</span>
			</div>
		</SettingsBlock>
	);
}

function UnitInput({
	label,
	unit,
	value,
	error,
	locked,
	reserve = true,
	describedBy,
	onChange,
}: {
	label: string;
	unit: string;
	value: string;
	error: string | undefined;
	locked: boolean;
	reserve?: boolean;
	describedBy: string | undefined;
	onChange: (value: string) => void;
}) {
	return (
		<Field label={label} error={error} reserve={reserve}>
			<InputGroup>
				<InputGroupInput
					className="font-mono"
					inputMode="numeric"
					aria-label={label}
					value={value}
					disabled={locked}
					aria-describedby={describedBy}
					aria-invalid={error !== undefined}
					onChange={(e) => {
						onChange(e.target.value);
					}}
				/>
				<InputGroupAddon
					align="inline-end"
					className="h-full self-stretch rounded-r-[inherit] border-l border-line2 bg-panel px-2.5"
				>
					<InputGroupText>{unit}</InputGroupText>
				</InputGroupAddon>
			</InputGroup>
		</Field>
	);
}

export function HealthSection({ draft, base, errors, locked, describedBy, onChange }: SectionProps) {
	return (
		<SettingsBlock id={HEALTH_SECTION_ID} title="Health check">
			<div className={grid2}>
				<Field label="Path" error={errors.healthPath} reserve>
					<Input
						className="font-mono"
						value={draft.healthPath ?? base.healthPath}
						disabled={locked}
						aria-describedby={describedBy}
						aria-invalid={errors.healthPath !== undefined}
						onChange={(e) => {
							onChange({ healthPath: e.target.value });
						}}
					/>
				</Field>
				<UnitInput
					label="Interval"
					unit="sec"
					value={draft.healthInterval ?? base.healthInterval}
					error={errors.healthInterval}
					locked={locked}
					describedBy={describedBy}
					onChange={(healthInterval) => {
						onChange({ healthInterval });
					}}
				/>
				<UnitInput
					label="Timeout"
					unit="sec"
					value={draft.healthTimeout ?? base.healthTimeout}
					error={errors.healthTimeout}
					locked={locked}
					describedBy={describedBy}
					onChange={(healthTimeout) => {
						onChange({ healthTimeout });
					}}
				/>
				<UnitInput
					label="Fail after"
					unit="checks"
					value={draft.healthFailures ?? base.healthFailures}
					error={errors.healthFailures}
					locked={locked}
					describedBy={describedBy}
					onChange={(healthFailures) => {
						onChange({ healthFailures });
					}}
				/>
			</div>
		</SettingsBlock>
	);
}

const RESTART_HINTS: Record<RestartChoice, string> = {
	"on-failure":
		"Loco restarts a replica that exits. Past the restart limit, a new version stops and the previous one keeps serving; a proven version keeps running and shows as crashed.",
	always: "Loco restarts a replica every time it exits.",
	never:
		"A replica that exits stops a new version and the previous one keeps serving; a proven version restarts it and shows as crashed.",
};

export function DeploymentSection({ draft, base, errors, locked, describedBy, onChange }: SectionProps) {
	const queryClient = useQueryClient();
	const restartPolicy = draft.restartPolicy ?? base.restartPolicy;
	return (
		<SettingsBlock id="set-deployment" title="Deployment behavior" hint="Changes apply on the next rollout.">
			{base.failurePolicyMissing ? (
				<ErrorState
					error={new Error("The API returned no failure policy for this service")}
					what="the failure policy"
					rpc={getResource}
					onRetry={async () => {
						await queryClient.refetchQueries();
					}}
				/>
			) : (
				<div className="flex flex-col gap-3.5">
					<Field label="Restart policy" hint={RESTART_HINTS[restartPolicy]}>
						<SegmentedChoice
							label="Restart policy"
							options={RESTART_CHOICES}
							value={restartPolicy}
							disabled={locked}
							describedBy={describedBy}
							className="w-max grid-cols-3"
							onChange={(next) => {
								const choice = RESTART_CHOICES.find((c) => c.value === next);
								if (choice !== undefined) onChange({ restartPolicy: choice.value });
							}}
						/>
					</Field>
					<div className={grid2}>
						{restartPolicy === "on-failure" && (
							<UnitInput
								label="Restart limit"
								unit="restarts"
								value={draft.maxRestarts ?? base.maxRestarts}
								error={errors.maxRestarts}
								locked={locked}
								reserve={false}
								describedBy={describedBy}
								onChange={(maxRestarts) => {
									onChange({ maxRestarts });
								}}
							/>
						)}
						<UnitInput
							label="Deploy timeout"
							unit="sec"
							value={draft.deployTimeout ?? base.deployTimeout}
							error={errors.deployTimeout}
							locked={locked}
							reserve={false}
							describedBy={describedBy}
							onChange={(deployTimeout) => {
								onChange({ deployTimeout });
							}}
						/>
					</div>
					<p className="m-0 text-[13px] text-fg3">
						A new version that is not healthy within the deploy timeout stops, and the previous version keeps serving. A
						version is proven once every replica has served for the cluster's health window, 5 minutes by default,
						without a restart.
					</p>
				</div>
			)}
		</SettingsBlock>
	);
}

export function ObservabilitySection({ draft, base, locked, describedBy, onChange }: SectionProps) {
	const next = draft.observability ?? base.observability;
	return (
		<SettingsBlock id="set-observability" title="Observability" hint="Changes apply on the next rollout.">
			<div className="flex items-start justify-between gap-4">
				<div className="flex min-w-0 flex-col gap-1">
					<label htmlFor="observability-injectOtel" className="text-[13.5px] font-medium">
						Configure SDK environment
					</label>
					<p id="observability-injectOtel-hint" className="m-0 text-[13px] text-fg3">
						Use Loco's collector connection and identity defaults. Your OpenTelemetry exporter or identity variables
						disable these defaults.
					</p>
				</div>
				<Switch
					id="observability-injectOtel"
					checked={next.injectOtel}
					disabled={locked}
					aria-describedby={["observability-injectOtel-hint", describedBy].filter(Boolean).join(" ")}
					onCheckedChange={(injectOtel: boolean) => {
						onChange({ observability: { injectOtel } });
					}}
				/>
			</div>
		</SettingsBlock>
	);
}

export function RegionsSection({
	draft,
	base,
	errors,
	locked,
	describedBy,
	onChange,
	available,
}: SectionProps & { available: RegionInfo[] }) {
	const defaultRegion = useQuery(getConfig, {}).data?.defaultRegion;
	const names = [...new Set([...available.map((r) => r.region), ...Object.keys(base.regions)])].toSorted();
	const hasDeployment = Object.keys(base.scaling).length > 0;
	const selected = names.filter((name) => draft.regions?.[name] ?? base.regions[name] === true);
	return (
		<SettingsBlock
			id="set-regions"
			title="Regions"
			hint={
				hasDeployment
					? "Regions apply in every environment this resource runs in. Turning a region off stops the resource there."
					: "Deploy this resource before adding regions."
			}
		>
			<RegionCards aria-label="Regions" value={selected}>
				{names.map((name) => {
					const configured = base.regions[name] === true;
					const info = available.find((region) => region.region === name);
					const allowed = configured || (hasDeployment && info?.healthStatus === "healthy");
					const on = selected.includes(name);
					const last = on && selected.length === 1;
					const note = !hasDeployment
						? undefined
						: configured
							? on
								? "Running"
								: "Will be removed"
							: on
								? "Will be added"
								: undefined;
					return (
						<RegionCard
							key={name}
							name={name}
							value={name}
							onPressedChange={(pressed, details) => {
								if (last) {
									details.cancel();
									return;
								}
								onChange({ regions: { ...draft.regions, [name]: pressed } });
							}}
							health={info}
							configured={configured}
							aria-label={`Run in ${name}`}
							aria-describedby={describedBy}
							disabled={locked || !allowed}
							aria-disabled={last || undefined}
							title={last ? "A resource needs at least one region" : undefined}
							noteTone={configured && !on ? "rm" : !configured && on ? "add" : undefined}
							note={
								note === undefined ? undefined : (
									<>
										{note}
										{configured && on && name === defaultRegion && ", default"}
									</>
								)
							}
						/>
					);
				})}
			</RegionCards>
			{errors.regions !== undefined && <span className="text-[12.5px] text-bad-fg">{errors.regions}</span>}
		</SettingsBlock>
	);
}

export function GeneralSection({
	draft,
	base,
	errors,
	locked,
	describedBy,
	onChange,
	canWrite,
	readOnlyId,
}: SectionProps & { canWrite: boolean; readOnlyId: string }) {
	return (
		<SettingsBlock id="set-general" title="General">
			<div className={grid2}>
				<Field label="Name" error={errors.name} hint="Shown in the dashboard. loco.yaml does not set it.">
					<Input
						value={draft.name ?? base.name}
						disabled={!canWrite}
						aria-describedby={canWrite ? undefined : readOnlyId}
						aria-invalid={errors.name !== undefined}
						onChange={(e) => {
							onChange({ name: e.target.value });
						}}
					/>
				</Field>
				<Field label="Description" hint="Shown under the name in lists and the resource switcher">
					<Input
						value={draft.description ?? base.description}
						placeholder="No description"
						disabled={locked}
						aria-describedby={describedBy}
						onChange={(e) => {
							onChange({ description: e.target.value });
						}}
					/>
				</Field>
			</div>
		</SettingsBlock>
	);
}
