import { Button } from "@/components/design/Button";
import { SectionFooter } from "@/components/design/Page";
import { cn } from "@/lib/utils";

export const PAGE_SIZES = [10, 25, 50, 100];

export function pageSlice<T>(rows: T[], page: number, size: number): { rows: T[]; page: number; pages: number } {
	const pages = Math.max(1, Math.ceil(rows.length / size));
	const p = Math.min(page, pages - 1);
	return { rows: rows.slice(p * size, p * size + size), page: p, pages };
}

export function Pager({
	page,
	pages,
	size,
	total,
	onPage,
	onSize,
}: {
	page: number;
	pages: number;
	size: number;
	total: number;
	onPage: (page: number) => void;
	onSize: (size: number) => void;
}) {
	const first = page * size + 1;
	const last = Math.min(total, (page + 1) * size);
	const label = total > 0 ? `${first.toString()}–${last.toString()} of ${total.toString()}` : "0 of 0";
	return (
		<SectionFooter className="flex-wrap gap-2 py-2">
			<div className="flex items-center gap-3">
				<span className="tabular-nums">{label}</span>
				<div className="flex items-center gap-1.5">
					Rows
					<div className="flex gap-0.5 rounded-sm border border-line p-0.5">
						{PAGE_SIZES.map((n) => (
							<button
								key={n}
								type="button"
								onClick={() => {
									onSize(n);
								}}
								className={cn(
									"h-5 cursor-pointer rounded-xs px-2 text-sm",
									size === n ? "bg-bg3 font-semibold text-foreground" : "text-fg3 hover:text-foreground",
								)}
							>
								{n}
							</button>
						))}
					</div>
				</div>
			</div>
			<div className="flex gap-1">
				<Button
					variant="outline"
					size="xs"
					disabled={page <= 0}
					onClick={() => {
						onPage(page - 1);
					}}
				>
					Previous
				</Button>
				<Button
					variant="outline"
					size="xs"
					disabled={page >= pages - 1}
					onClick={() => {
						onPage(page + 1);
					}}
				>
					Next
				</Button>
			</div>
		</SectionFooter>
	);
}
