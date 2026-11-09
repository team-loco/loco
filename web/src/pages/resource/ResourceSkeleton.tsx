import { Page } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";

export function ResourceSkeleton() {
	return (
		<Page className="gap-5">
			<div className="flex flex-wrap items-end justify-between gap-4">
				<div className="flex flex-col gap-2">
					<div className="flex items-center gap-3">
						<Skeleton className="h-8 w-48" />
						<Skeleton className="h-5 w-20" />
					</div>
					<div className="flex gap-[18px]">
						<Skeleton className="h-4 w-36" />
						<Skeleton className="h-4 w-24" />
						<Skeleton className="h-4 w-40" />
					</div>
				</div>
				<div className="flex gap-2">
					<Skeleton className="h-8 w-20" />
					<Skeleton className="h-8 w-24" />
					<Skeleton className="h-8 w-24" />
					<Skeleton className="size-8" />
				</div>
			</div>
			<div className="flex flex-col gap-4 rounded-[10px] bg-panel px-5 py-[18px]">
				<Skeleton className="h-5 w-56" />
				<Skeleton className="h-8 w-full" />
				<div className="grid grid-cols-1 gap-7 lg:grid-cols-2">
					<Skeleton className="h-48" />
					<Skeleton className="h-48" />
				</div>
			</div>
			<div className="flex flex-col gap-3 rounded-[10px] bg-panel px-5 py-4">
				<Skeleton className="h-6 w-40" />
				{[0, 1, 2, 3].map((i) => (
					<Skeleton key={i} className="h-10 w-full" />
				))}
			</div>
		</Page>
	);
}
