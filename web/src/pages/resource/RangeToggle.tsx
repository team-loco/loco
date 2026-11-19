import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";

import { METRIC_RANGES, type MetricRange } from "./metric";

export function RangeToggle({ range, onRange }: { range: MetricRange; onRange: (range: MetricRange) => void }) {
	return (
		<div className="flex items-center justify-end gap-2 text-[12.5px] text-fg3">
			<span>Charts show the last</span>
			<ToggleGroup
				variant="segmented"
				aria-label="Time range"
				value={[range]}
				onValueChange={(v: string[]) => {
					const next = METRIC_RANGES.find((r) => r === v[0]);
					if (next !== undefined) onRange(next);
				}}
			>
				{METRIC_RANGES.map((r) => (
					<ToggleGroupItem key={r} value={r}>
						{r}
					</ToggleGroupItem>
				))}
			</ToggleGroup>
		</div>
	);
}
