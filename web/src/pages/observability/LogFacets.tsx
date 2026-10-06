import { CheckIcon } from "lucide-react";

import { SoonTag } from "@/components/design/SoonTag";
import { formatCount } from "@/lib/format";
import { cn } from "@/lib/utils";

import { useObs } from "./context";
import { levelStyle } from "./format";
import { LEVELS, sameToken, type FieldKey } from "./query";
import type { LogRow } from "./rows";
import { Dot } from "./shared";

interface FacetValue {
	value: string;
	count: number;
	dot: string | null;
	dotClass: string | null;
}

export function LogFacets({ rows }: { rows: LogRow[] }) {
	const { tokens, setTokens, resources, clusters } = useObs();

	const levelCounts = new Map<string, number>();
	const resCounts = new Map<string, number>();
	const regionCounts = new Map<string, number>();
	const bump = (m: Map<string, number>, v: string) => {
		if (v !== "") m.set(v, (m.get(v) ?? 0) + 1);
	};
	for (const r of rows) {
		bump(levelCounts, r.level);
		bump(resCounts, r.resourceName);
		bump(regionCounts, r.region);
	}
	const regions = [...new Set(clusters.map((c) => c.region))];

	const facets: { key: FieldKey; title: string; values: FacetValue[] }[] = [
		{
			key: "level",
			title: "Level",
			values: LEVELS.map((l) => ({ value: l, count: levelCounts.get(l) ?? 0, dot: null, dotClass: levelStyle(l).dot })),
		},
		{
			key: "resource",
			title: "Resource",
			values: resources.map((r) => ({ value: r.name, count: resCounts.get(r.name) ?? 0, dot: r.color, dotClass: null })),
		},
		{
			key: "region",
			title: "Region",
			values: regions.map((g) => ({ value: g, count: regionCounts.get(g) ?? 0, dot: null, dotClass: null })),
		},
	];

	return (
		<aside className="sticky top-4 flex flex-col gap-[18px]">
			{facets.map((fc) => (
				<div key={fc.key} className="flex flex-col gap-px">
					<div className="px-1.5 pb-1.5 text-sm font-semibold text-fg2">{fc.title}</div>
					{fc.values.length === 0 && <div className="px-1.5 text-sm text-fg4">None</div>}
					{fc.values.map((v) => {
						const tok = { neg: false, key: fc.key, value: v.value };
						const on = tokens.some((t) => sameToken(t, tok));
						return (
							<button
								key={v.value}
								type="button"
								onClick={() => {
									setTokens(on ? tokens.filter((t) => !sameToken(t, tok)) : [...tokens, tok]);
								}}
								className={cn(
									"flex h-7 cursor-pointer items-center gap-2 rounded-sm px-1.5 text-left text-foreground hover:bg-bg3",
									v.count === 0 && !on && "opacity-45",
								)}
							>
								<span
									className={cn(
										"flex size-3.5 shrink-0 items-center justify-center rounded-[3px] border text-white",
										on ? "border-primary bg-primary" : "border-line2 bg-background",
									)}
								>
									{on && <CheckIcon className="size-2.5" />}
								</span>
								{v.dotClass !== null && <span className={cn("size-[7px] shrink-0 rounded-[2px]", v.dotClass)} />}
								{v.dot !== null && <Dot color={v.dot} />}
								<span className="min-w-0 flex-1 truncate">{v.value}</span>
								<span className="text-xs text-fg3 tabular-nums">{formatCount(v.count)}</span>
							</button>
						);
					})}
				</div>
			))}
			<div className="flex flex-col gap-px">
				<div className="flex items-center gap-1.5 px-1.5 pb-1.5 text-sm font-semibold text-fg4">
					Status
					<SoonTag />
				</div>
			</div>
		</aside>
	);
}
