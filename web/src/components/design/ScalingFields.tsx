import type { Scaling } from "@/lib/new-resource";
import type { ReplicaRange } from "@/lib/sizing";

import { Field } from "./Field";
import { Stepper } from "./Stepper";

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
	return (
		<>
			<div className="grid grid-cols-2 gap-4">
				<Field label="Minimum replicas">
					<Stepper
						label={`Minimum replicas${suffix}`}
						value={value.replicas}
						min={replicaRange?.min ?? value.replicas}
						max={replicaRange?.max ?? value.replicas}
						disabled={disabled}
						onChange={(replicas) => {
							onChange({ ...value, replicas, maxReplicas: Math.max(replicas, value.maxReplicas) });
						}}
					/>
				</Field>
				<Field label="Maximum replicas">
					<Stepper
						label={`Maximum replicas${suffix}`}
						value={value.maxReplicas}
						min={value.replicas}
						max={replicaRange?.max ?? value.maxReplicas}
						disabled={disabled}
						onChange={(maxReplicas) => {
							onChange({ ...value, maxReplicas });
						}}
					/>
				</Field>
				{value.maxReplicas !== value.replicas && (
					<Field label="CPU target (%)">
						<Stepper
							label={`CPU target (%)${suffix}`}
							value={value.cpuTarget}
							min={1}
							max={100}
							disabled={disabled}
							onChange={(cpuTarget) => {
								onChange({ ...value, cpuTarget });
							}}
						/>
					</Field>
				)}
			</div>
			<p className="mt-2 text-[12.5px] text-fg3">
				{value.maxReplicas === value.replicas
					? `Runs exactly ${value.replicas.toString()} per region.`
					: "Scales per region based on CPU utilization."}
			</p>
			{error != null && (
				<p role="alert" className="text-[12.5px] text-bad-fg">
					{error}
				</p>
			)}
		</>
	);
}
