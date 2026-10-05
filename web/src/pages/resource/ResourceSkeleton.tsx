import { Page } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";

export function ResourceSkeleton() {
	return (
		<Page className="gap-5">
			<div className="flex flex-col gap-2">
				<div className="flex items-center gap-3">
					<Skeleton className="h-7 w-44" />
					<Skeleton className="h-[22px] w-16" />
				</div>
				<Skeleton className="h-4 w-56" />
				<div className="mt-1.5 flex gap-[18px]">
					<Skeleton className="h-4 w-40" />
					<Skeleton className="h-4 w-32" />
					<Skeleton className="h-4 w-24" />
					<Skeleton className="h-4 w-24" />
				</div>
			</div>
			<div className="flex items-center gap-2.5 border-b border-line pb-4">
				<Skeleton className="h-[38px] w-[330px] rounded-xl" />
				<div className="flex-1" />
				<Skeleton className="h-8 w-56" />
				<Skeleton className="h-8 w-20" />
			</div>
			<div className="rounded-lg border border-line">
				<div className="flex items-center justify-between border-b border-line px-4 py-2.5">
					<Skeleton className="h-[30px] w-32" />
					<Skeleton className="h-[30px] w-28" />
				</div>
				<div className="border-b border-line px-4 py-2.5">
					<Skeleton className="h-[22px] w-64" />
				</div>
				<div className="grid grid-cols-[minmax(220px,0.8fr)_minmax(0,1fr)_minmax(0,1fr)] gap-4 p-4">
					<Skeleton className="h-36" />
					<Skeleton className="h-36" />
					<Skeleton className="h-36" />
				</div>
			</div>
			<div className="rounded-lg border border-line">
				<div className="border-b border-line px-4 py-3">
					<Skeleton className="h-5 w-32" />
				</div>
				{[0, 1, 2, 3].map((i) => (
					<div key={i} className="flex gap-3 border-b border-line px-4 py-3">
						<Skeleton className="h-4 w-28" />
						<Skeleton className="h-4 w-16" />
						<Skeleton className="h-4 w-20" />
						<Skeleton className="h-4 flex-1" />
					</div>
				))}
			</div>
		</Page>
	);
}
