import { SearchInput } from "@/components/design/SearchInput";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";

import { ALL, statusCounts, visibleStatusFilters, type Filter, type ListRow } from "./table";

export function StatusFilter({
	rows,
	filter,
	onStatus,
}: {
	rows: ListRow[];
	filter: Filter;
	onStatus: (status: string) => void;
}) {
	const counts = statusCounts(rows, filter);
	return (
		<ToggleGroup
			variant="segmented"
			aria-label="Filter by status"
			className="max-w-full overflow-x-auto"
			value={[filter.status]}
			onValueChange={(v: string[]) => {
				if (v[0] !== undefined) onStatus(v[0]);
			}}
		>
			<ToggleGroupItem value={ALL} count={counts.get(ALL)}>
				All
			</ToggleGroupItem>
			{visibleStatusFilters(counts, filter.status).map((f) => (
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
