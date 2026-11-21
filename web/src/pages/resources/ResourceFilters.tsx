import { SearchInput } from "@/components/design/SearchInput";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";

import { ALL, visibleStatusFilters } from "./table";

export function StatusFilter({
	counts,
	status,
	onStatus,
}: {
	counts: ReadonlyMap<string, number>;
	status: string;
	onStatus: (status: string) => void;
}) {
	return (
		<ToggleGroup
			variant="segmented"
			aria-label="Filter by status"
			className="max-w-full overflow-x-auto"
			value={[status]}
			onValueChange={(v: string[]) => {
				if (v[0] !== undefined) onStatus(v[0]);
			}}
		>
			<ToggleGroupItem value={ALL} count={counts.get(ALL)}>
				All
			</ToggleGroupItem>
			{visibleStatusFilters(counts, status).map((f) => (
				<ToggleGroupItem key={f.value} value={f.value} count={counts.get(f.value)}>
					{f.label}
				</ToggleGroupItem>
			))}
		</ToggleGroup>
	);
}

export function ResourceSearch({ query, onQuery }: { query: string; onQuery: (query: string) => void }) {
	return (
		<SearchInput
			value={query}
			placeholder="Search name, image, version"
			aria-label="Search resources"
			onChange={(e) => {
				onQuery(e.target.value);
			}}
			onClear={() => {
				onQuery("");
			}}
		/>
	);
}
