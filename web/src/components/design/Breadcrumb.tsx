import {
	Breadcrumb,
	BreadcrumbEllipsis,
	BreadcrumbItem,
	BreadcrumbLink as BreadcrumbLinkBase,
	BreadcrumbList as BreadcrumbListBase,
	BreadcrumbPage as BreadcrumbPageBase,
	BreadcrumbSeparator as BreadcrumbSeparatorBase,
} from "@/components/ui/breadcrumb"
import { cn } from "@/lib/utils"

function BreadcrumbList({ className, ...props }: React.ComponentProps<typeof BreadcrumbListBase>) {
	return <BreadcrumbListBase className={cn("flex-nowrap gap-1.5 text-md text-fg3 sm:gap-1.5", className)} {...props} />
}

function BreadcrumbLink({ className, ...props }: React.ComponentProps<typeof BreadcrumbLinkBase>) {
	return <BreadcrumbLinkBase className={cn("truncate hover:text-foreground", className)} {...props} />
}

function BreadcrumbPage({ className, ...props }: React.ComponentProps<typeof BreadcrumbPageBase>) {
	return <BreadcrumbPageBase className={cn("truncate font-normal text-foreground", className)} {...props} />
}

function BreadcrumbSeparator({ className, children, ...props }: React.ComponentProps<typeof BreadcrumbSeparatorBase>) {
	return (
		<BreadcrumbSeparatorBase className={cn("text-fg4", className)} {...props}>
			{children ?? "›"}
		</BreadcrumbSeparatorBase>
	)
}

export { Breadcrumb, BreadcrumbEllipsis, BreadcrumbItem, BreadcrumbLink, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator }
