import {
	Tabs as TabsBase,
	TabsContent,
	TabsList as TabsListBase,
	TabsTrigger as TabsTriggerBase,
} from "@/components/ui/tabs"
import { cn } from "@/lib/utils"

function Tabs({ className, ...props }: React.ComponentProps<typeof TabsBase>) {
	return <TabsBase className={cn("gap-4", className)} {...props} />
}

function TabsList({ className, ...props }: Omit<React.ComponentProps<typeof TabsListBase>, "variant">) {
	return (
		<TabsListBase
			variant="line"
			className={cn("h-auto! w-full justify-start gap-5 rounded-none border-b border-line p-0", className)}
			{...props}
		/>
	)
}

function TabsTrigger({ className, ...props }: React.ComponentProps<typeof TabsTriggerBase>) {
	return (
		<TabsTriggerBase
			className={cn(
				"h-9 flex-none rounded-none border-0 px-0 text-md font-normal text-fg3 hover:text-foreground data-active:font-medium data-active:text-foreground dark:text-fg3 group-data-horizontal/tabs:after:bottom-[-1px] [&_svg:not([class*='size-'])]:size-3.5",
				className
			)}
			{...props}
		/>
	)
}

export { Tabs, TabsList, TabsTrigger, TabsContent }
