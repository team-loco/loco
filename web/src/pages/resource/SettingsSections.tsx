import type { ReactNode } from "react";
import type { RegionInfo } from "@gen/loco/resource/v1/resource_pb";

import { RegionCard, RegionCards } from "@/components/design/RegionCards";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { StepSlider } from "@/components/design/StepSlider";
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from "@/components/design/InputGroup";
import { ScalingFields } from "@/components/design/ScalingFields";
import { Switch } from "@/components/design/Switch";
import { joinImage, splitImage } from "@/lib/image";
import type { SizeOptions } from "@/lib/sizing";
import { slug } from "@/lib/slug";

import type { RegionView } from "./model";
import type { Baseline, DraftErrors, SettingsDraft } from "./settings-draft";

export const HEALTH_SECTION_ID = "set-health";

export interface SectionProps {
	draft: SettingsDraft;
	base: Baseline;
	errors: DraftErrors;
	locked: boolean;
	describedBy: string | undefined;
	onChange: (patch: Partial<SettingsDraft>) => void;
}

const grid2 = "grid grid-cols-[repeat(auto-fit,minmax(min(220px,100%),1fr))] gap-x-[18px] gap-y-3.5";

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
	const { repository: path, reference: version } = splitImage(draft.image ?? base.image);
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
				<Field label="Tag or digest">
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

export function ScalingSection({
	draft,
	base,
	errors,
	locked,
	onChange,
	sizes,
	regions,
	sectionRef,
}: SectionProps & {
	sizes: SizeOptions;
	regions: RegionView[];
	sectionRef: ((node: HTMLElement | null) => void) | undefined;
}) {
	const names = Object.keys(base.scaling);
	return (
		<SettingsBlock
			id="set-scaling"
			sectionRef={sectionRef}
			title="Scaling"
			hint="Set equal bounds for fixed replicas or a range for CPU autoscaling."
		>
			{names.length === 0 && <span className="text-fg3">Scaling is available once the resource is deployed.</span>}
			<div className="flex flex-col gap-5">
				{Object.entries(base.scaling).map(([name, value]) => (
					<div key={name} className="flex flex-col gap-1.5">
						<span className="font-mono text-[12.5px] text-fg2">{name}</span>
						<ScalingFields
							value={draft.scaling?.[name] ?? value}
							region={name}
							replicaRange={sizes.replicas}
							disabled={locked}
							error={errors[`scaling.${name}`] ?? null}
							onChange={(scaling) => {
								onChange({ scaling: { ...draft.scaling, [name]: scaling } });
							}}
						/>
					</div>
				))}
			</div>
			{regions.length > names.length && (
				<span className="text-[12.5px] text-fg3">
					Regions added below start with the scaling of {names[0] ?? "the first region"}.
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
	return (
		<SettingsBlock id="set-size" title="Size per replica" hint="Requests for each replica, applied in every region.">
			<div className="flex flex-col gap-3.5">
				<Field label="CPU">
					<StepSlider
						label="CPU"
						options={sizes.cpu}
						value={draft.cpu ?? base.cpu}
						disabled={locked}
						describedBy={describedBy}
						onChange={(cpu) => {
							onChange({ cpu });
						}}
					/>
				</Field>
				<Field label="Memory">
					<StepSlider
						label="Memory"
						options={sizes.memory}
						value={draft.memory ?? base.memory}
						disabled={locked}
						describedBy={describedBy}
						onChange={(memory) => {
							onChange({ memory });
						}}
					/>
				</Field>
			</div>
		</SettingsBlock>
	);
}

function SecondsInput({
	label,
	unit,
	value,
	error,
	locked,
	describedBy,
	onChange,
}: {
	label: string;
	unit: string;
	value: string;
	error: string | undefined;
	locked: boolean;
	describedBy: string | undefined;
	onChange: (value: string) => void;
}) {
	return (
		<Field label={label} error={error}>
			<InputGroup>
				<InputGroupInput
					className="font-mono"
					inputMode="numeric"
					value={value}
					disabled={locked}
					aria-describedby={describedBy}
					aria-invalid={error !== undefined}
					onChange={(e) => {
						onChange(e.target.value);
					}}
				/>
				<InputGroupAddon align="inline-end">
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
				<Field label="Path" error={errors.healthPath}>
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
				<SecondsInput
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
				<SecondsInput
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
				<SecondsInput
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
										{configured && on && info?.isDefault === true && " · default"}
										{on && !configured && (
											<span className="block text-warn-fg">Starts without this resource's secrets</span>
										)}
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

export function GeneralSection({ draft, base, errors, locked, describedBy, onChange }: SectionProps) {
	const name = draft.name ?? base.name;
	return (
		<SettingsBlock id="set-general" title="General">
			<div className={grid2}>
				<Field label="Name" error={errors.name} hint={`Private hostname: ${slug(name) || "none"}`}>
					<Input
						value={name}
						disabled={locked}
						aria-describedby={describedBy}
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
