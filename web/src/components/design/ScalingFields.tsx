import type { Scaling } from "@/lib/new-resource";
import type { ReplicaRange } from "@/lib/sizing";

import { Field } from "./Field";
import { RangeSlider } from "./RangeSlider";
import { SoonTag } from "./SoonTag";
import { Stepper } from "./Stepper";
import { Switch } from "./Switch";

const MEMORY_TARGET = 80;

export function autoscales(value: Scaling): boolean {
	return value.maxReplicas > value.replicas;
}

export function AutoscalingSwitch({
	value,
	replicaRange,
	onChange,
	disabled = false,
	region,
}: {
	value: Scaling;
	replicaRange: ReplicaRange | null;
	onChange: (value: Scaling) => void;
	disabled?: boolean;
	region?: string;
}) {
	const on = autoscales(value);
	const limit = replicaRange?.max ?? value.maxReplicas;
	return (
		<span className="flex items-center gap-2.5 text-[13px] text-fg2">
			Autoscaling
			<Switch
				aria-label={region === undefined ? "Autoscaling" : `Autoscaling in ${region}`}
				checked={on}
				disabled={disabled || (!on && value.replicas >= limit)}
				onCheckedChange={(next) => {
					onChange({ ...value, maxReplicas: next ? Math.min(limit, value.replicas + 1) : value.replicas });
				}}
			/>
		</span>
	);
}

export function ScalingFields({
	value,
	replicaRange,
	onChange,
	disabled = false,
	region,
	error,
}: {
	value: Scaling;
	replicaRange: ReplicaRange | null;
	onChange: (value: Scaling) => void;
	disabled?: boolean;
	region?: string;
	error?: string | null;
}) {
	const suffix = region === undefined ? "" : ` in ${region}`;
	const limit = replicaRange?.max ?? value.maxReplicas;
	const floor = replicaRange?.min ?? value.replicas;
	return (
		<>
			{autoscales(value) ? (
				<div className="grid grid-cols-2 gap-x-4 gap-y-3">
					<Field label="Min replicas per region">
						<Stepper
							label={`Minimum replicas${suffix}`}
							value={value.replicas}
							min={floor}
							max={value.maxReplicas - 1}
							disabled={disabled}
							onChange={(replicas) => {
								onChange({ ...value, replicas });
							}}
						/>
					</Field>
					<Field label="Max replicas per region">
						<Stepper
							label={`Maximum replicas${suffix}`}
							value={value.maxReplicas}
							min={value.replicas + 1}
							max={limit}
							disabled={disabled}
							onChange={(maxReplicas) => {
								onChange({ ...value, maxReplicas });
							}}
						/>
					</Field>
					<RangeSlider
						label={`CPU target (%)${suffix}`}
						heading="CPU target"
						value={value.cpuTarget}
						min={1}
						max={100}
						disabled={disabled}
						onChange={(cpuTarget) => {
							onChange({ ...value, cpuTarget });
						}}
					/>
					<RangeSlider
						label={`Memory target (%)${suffix}`}
						heading={
							<span className="flex items-center gap-2">
								Memory target
								<SoonTag />
							</span>
						}
						value={MEMORY_TARGET}
						min={1}
						max={100}
						disabled
						onChange={() => undefined}
					/>
				</div>
			) : (
				<Field label="Replicas per region">
					<Stepper
						label={`Replicas${suffix}`}
						value={value.replicas}
						min={floor}
						max={limit}
						disabled={disabled}
						onChange={(replicas) => {
							onChange({ ...value, replicas, maxReplicas: replicas });
						}}
					/>
				</Field>
			)}
			<p className="m-0 text-[12.5px] text-fg3">
				{autoscales(value)
					? "Scales per region based on CPU utilization."
					: `Runs exactly ${value.replicas.toString()} per region.`}
			</p>
			{error != null && (
				<p role="alert" className="text-[12.5px] text-bad-fg">
					{error}
				</p>
			)}
		</>
	);
}
