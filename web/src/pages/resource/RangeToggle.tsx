import { SoonTag } from "@/components/design/SoonTag";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";

import { METRIC_RANGES, type MetricRange } from "./metric";

const UNSUPPORTED_RANGE = "7d";

export function RangeToggle({ range, onRange }: { range: MetricRange; onRange: (range: MetricRange) => void }) {
	return (
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
			<ToggleGroupItem value={UNSUPPORTED_RANGE} disabled>
				{UNSUPPORTED_RANGE}
				<SoonTag />
			</ToggleGroupItem>
		</ToggleGroup>
	);
}
