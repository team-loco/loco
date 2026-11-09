import {
	Breadcrumb,
	BreadcrumbItem,
	BreadcrumbList as BreadcrumbListBase,
	BreadcrumbSeparator as BreadcrumbSeparatorBase,
} from "@/components/ui/breadcrumb";
import { cn } from "@/lib/utils";

function BreadcrumbList({ className, ...props }: React.ComponentProps<typeof BreadcrumbListBase>) {
	return <BreadcrumbListBase className={cn("flex-nowrap gap-0.5 text-fg3 sm:gap-0.5", className)} {...props} />;
}

function BreadcrumbSeparator({ className, children, ...props }: React.ComponentProps<typeof BreadcrumbSeparatorBase>) {
	return (
		<BreadcrumbSeparatorBase className={cn("text-fg4", className)} {...props}>
			{children ?? "›"}
		</BreadcrumbSeparatorBase>
	);
}

export { Breadcrumb, BreadcrumbItem, BreadcrumbList, BreadcrumbSeparator };
