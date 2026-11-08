import { Skeleton as SkeletonBase } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

function Skeleton({ className, ...props }: React.ComponentProps<typeof SkeletonBase>) {
	return <SkeletonBase className={cn("rounded-md bg-panel2", className)} {...props} />;
}

export { Skeleton };
