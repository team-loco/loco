import { HistoryIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import type { TimeRange } from "@/lib/obs";

import { useObs } from "./context";

export function WidenRangeButton({ max }: { max: TimeRange }) {
	const { setRange } = useObs();
	return (
		<Button
			variant="outline"
			onClick={() => {
				setRange(max);
			}}
		>
			<HistoryIcon />
			{`Show last ${max}`}
		</Button>
	);
}
