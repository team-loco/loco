import {
	Tabs as TabsBase,
	TabsContent as TabsContentBase,
	TabsList as TabsListBase,
	TabsTrigger as TabsTriggerBase,
} from "@/components/ui/tabs";
import { cn } from "@/lib/utils";

function Tabs({ className, ...props }: React.ComponentProps<typeof TabsBase>) {
	return <TabsBase className={cn("gap-0", className)} {...props} />;
}

function TabsList({ className, ...props }: React.ComponentProps<typeof TabsListBase>) {
	return (
		<TabsListBase
			variant="line"
			className={cn("h-auto! w-full justify-start gap-0.5 rounded-none border-b border-line p-0", className)}
			{...props}
		/>
	);
}

function TabsTrigger({ className, ...props }: React.ComponentProps<typeof TabsTriggerBase>) {
	return (
		<TabsTriggerBase
			className={cn(
				"-mb-px h-[34px] flex-none gap-2 rounded-none border-0 border-b-2 border-transparent px-2.5 text-[14px] font-medium text-fg3 after:hidden hover:text-foreground data-active:border-foreground data-active:text-foreground dark:text-fg3 dark:data-active:border-foreground",
				className,
			)}
			{...props}
		/>
	);
}

function TabsCount({ className, ...props }: React.ComponentProps<"span">) {
	return <span className={cn("font-mono text-[11.5px] font-normal text-fg3 tabular-nums", className)} {...props} />;
}

function TabsContent({ className, ...props }: React.ComponentProps<typeof TabsContentBase>) {
	return <TabsContentBase className={cn("text-[13px]", className)} {...props} />;
}

export { Tabs, TabsContent, TabsCount, TabsList, TabsTrigger };
