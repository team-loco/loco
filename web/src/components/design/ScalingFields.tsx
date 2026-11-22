import type { Scaling } from "@/lib/new-resource";
import type { ReplicaRange } from "@/lib/sizing";

import { Field } from "./Field";
import { RangeSlider } from "./RangeSlider";
import { Stepper } from "./Stepper";
import { Switch } from "./Switch";

export function autoscales(value: Scaling): boolean {
	return value.maxReplicas > value.replicas;
}

export function AutoscalingSwitch({
	value,
	replicaRange,
	onChange,
	disabled = false,
}: {
	value: Scaling;
	replicaRange: ReplicaRange | null;
	onChange: (value: Scaling) => void;
	disabled?: boolean;
}) {
	const on = autoscales(value);
	const limit = replicaRange?.max ?? value.maxReplicas;
	return (
		<span className="flex items-center gap-2.5 text-[13px] text-fg2">
			Autoscaling
			<Switch
				aria-label="Autoscaling"
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
	error,
}: {
	value: Scaling;
	replicaRange: ReplicaRange | null;
	onChange: (value: Scaling) => void;
	disabled?: boolean;
	error?: string | null;
}) {
	const autoscaling = autoscales(value);
	const limit = replicaRange?.max ?? value.maxReplicas;
	const floor = replicaRange?.min ?? value.replicas;
	return (
		<>
			{autoscaling ? (
				<div className="grid max-w-[520px] grid-cols-2 gap-x-4 gap-y-3">
					<Field label="Min replicas per region">
						<Stepper
							label="Minimum replicas"
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
							label="Maximum replicas"
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
						label="CPU target (%)"
						heading="CPU target"
						value={value.cpuTarget}
						min={10}
						max={95}
						step={5}
						disabled={disabled}
						onChange={(cpuTarget) => {
							onChange({ ...value, cpuTarget });
						}}
					/>
				</div>
			) : (
				<Field label="Replicas per region">
					<Stepper
						label="Replicas"
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
			{autoscaling && (
				<p className="m-0 text-[12.5px] text-fg3">
					Adds replicas when average CPU stays above {value.cpuTarget}%, and removes them when it drops, between{" "}
					{value.replicas} and {value.maxReplicas} per region.
				</p>
			)}
			{error != null && (
				<p role="alert" className="text-[12.5px] text-bad-fg">
					{error}
				</p>
			)}
		</>
	);
}
