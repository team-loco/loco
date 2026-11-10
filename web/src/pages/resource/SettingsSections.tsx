import type { ReactNode } from "react";
import type { RegionInfo } from "@gen/loco/resource/v1/resource_pb";

import { RegionHealth } from "@/components/design/RegionHealth";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from "@/components/design/InputGroup";
import { SoonTag } from "@/components/design/SoonTag";
import { Stepper } from "@/components/design/Stepper";
import { Switch } from "@/components/design/Switch";
import { slug } from "@/lib/slug";

import type { RegionView } from "./model";
import type { Baseline, DraftErrors, SettingsDraft } from "./settings-draft";

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
	return (
		<SettingsBlock
			id="set-image"
			title="Image"
			hint="Public images only. Loco pins the image by digest when it deploys."
		>
			<Field label="Image reference" error={errors.image}>
				<Input
					className="font-mono"
					value={draft.image ?? base.image}
					disabled={locked}
					aria-describedby={describedBy}
					aria-invalid={errors.image !== undefined}
					spellCheck={false}
					onChange={(e) => {
						onChange({ image: e.target.value });
					}}
				/>
			</Field>
		</SettingsBlock>
	);
}

export function ScalingSection({
	draft,
	base,
	locked,
	onChange,
	regions,
	sectionRef,
}: SectionProps & { regions: RegionView[]; sectionRef: ((node: HTMLElement | null) => void) | undefined }) {
	const names = Object.keys(base.replicas);
	return (
		<SettingsBlock
			id="set-scaling"
			sectionRef={sectionRef}
			title="Scaling"
			hint="A fixed number of replicas in each region."
			actions={
				<span className="flex items-center gap-2 text-[13px] text-fg3">
					Autoscaling
					<SoonTag />
					<Switch disabled checked={false} aria-label="Autoscaling" />
				</span>
			}
		>
			{names.length === 0 && <span className="text-fg3">Scaling is available once the resource is deployed.</span>}
			<div className="flex flex-wrap gap-x-8 gap-y-4">
				{names.map((name) => (
					<div key={name} className="flex flex-col gap-1.5">
						<span className="font-mono text-[12.5px] text-fg2">{name}</span>
						<Stepper
							label={`Replicas in ${name}`}
							value={draft.replicas?.[name] ?? base.replicas[name] ?? 1}
							min={1}
							max={Number.POSITIVE_INFINITY}
							disabled={locked}
							onChange={(n) => {
								onChange({ replicas: { ...draft.replicas, [name]: n } });
							}}
						/>
					</div>
				))}
			</div>
			{regions.length > names.length && (
				<span className="text-[12.5px] text-fg3">
					Regions added below start with the replicas of {names[0] ?? "the first region"}.
				</span>
			)}
		</SettingsBlock>
	);
}

export function SizeSection({ draft, base, errors, locked, describedBy, onChange }: SectionProps) {
	return (
		<SettingsBlock id="set-size" title="Size per replica" hint="Requests for each replica, applied in every region.">
			<div className={grid2}>
				<Field label="CPU" error={errors.cpu} hint="Cores, like 250m or 1">
					<Input
						className="font-mono"
						value={draft.cpu ?? base.cpu}
						disabled={locked}
						aria-describedby={describedBy}
						aria-invalid={errors.cpu !== undefined}
						onChange={(e) => {
							onChange({ cpu: e.target.value });
						}}
					/>
				</Field>
				<Field label="Memory" error={errors.memory} hint="Like 512Mi or 1Gi">
					<Input
						className="font-mono"
						value={draft.memory ?? base.memory}
						disabled={locked}
						aria-describedby={describedBy}
						aria-invalid={errors.memory !== undefined}
						onChange={(e) => {
							onChange({ memory: e.target.value });
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
		<SettingsBlock id="set-health" title="Health check">
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

function regionNote(info: RegionInfo | undefined, inSpec: boolean, deployed: boolean): ReactNode {
	return (
		<span className="flex flex-wrap items-center gap-2">
			<RegionHealth region={info} />
			{!inSpec
				? "Not in this resource's spec"
				: !deployed
					? "Starts without this resource's secrets"
					: info?.isDefault === true
						? "default"
						: null}
		</span>
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
	inSpec,
}: SectionProps & { available: RegionInfo[]; inSpec: string[] }) {
	const names = [...new Set([...available.map((r) => r.region), ...Object.keys(base.regions)])].toSorted();
	return (
		<SettingsBlock id="set-regions" title="Regions" hint="Turning a region off stops the resource there.">
			<div className="overflow-hidden rounded-card border border-line bg-background">
				{names.map((name) => {
					const deployed = base.regions[name] === true;
					const allowed = deployed || inSpec.includes(name);
					const on = draft.regions?.[name] ?? deployed;
					return (
						<div key={name} className="flex items-center gap-3.5 border-b border-line px-3.5 py-3 last:border-b-0">
							<span className="font-mono text-[12.5px]">{name}</span>
							<span className="flex-1 text-[12.5px] text-fg3">
								{regionNote(
									available.find((r) => r.region === name),
									allowed,
									deployed,
								)}
							</span>
							<Switch
								checked={on}
								disabled={locked || !allowed}
								aria-label={`Run in ${name}`}
								aria-describedby={describedBy}
								onCheckedChange={(checked: boolean) => {
									onChange({ regions: { ...draft.regions, [name]: checked } });
								}}
							/>
						</div>
					);
				})}
			</div>
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
	description,
}: SectionProps & { description: string }) {
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
				<Field
					label={
						<span className="flex items-center gap-1.5">
							Description
							<SoonTag />
						</span>
					}
				>
					<Input value={description} placeholder="No description" disabled readOnly />
				</Field>
			</div>
		</SettingsBlock>
	);
}
