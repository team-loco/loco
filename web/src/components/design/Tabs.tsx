import { createContext, use } from "react";
import { SegmentedScope, SegmentedThumb, segmentedGroupClass, segmentedItemClass } from "./Segmented";
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

type TabsVariant = "line" | "segmented";
const TabsVariantContext = createContext<TabsVariant>("line");

function TabsList({
	className,
	variant = "line",
	children,
	...props
}: Omit<React.ComponentProps<typeof TabsListBase>, "variant"> & { variant?: TabsVariant }) {
	const segmented = variant === "segmented";
	const list = (
		<TabsListBase
			variant="line"
			data-segmented={segmented || undefined}
			className={cn(
				segmented
					? cn("h-auto! w-fit justify-start gap-0.5 border-0", segmentedGroupClass)
					: "h-auto! w-full justify-start gap-0.5 rounded-none border-b border-line p-0",
				className,
			)}
			{...props}
		>
			{children}
		</TabsListBase>
	);
	return (
		<TabsVariantContext value={variant}>
			{segmented ? <SegmentedScope>{list}</SegmentedScope> : list}
		</TabsVariantContext>
	);
}

function TabsTrigger({ className, ...props }: React.ComponentProps<typeof TabsTriggerBase>) {
	const segmented = use(TabsVariantContext) === "segmented";
	return (
		<TabsTriggerBase
			render={
				segmented
					? (buttonProps, state) => (
							<button type="button" {...buttonProps}>
								{state.active && <SegmentedThumb />}
								{buttonProps.children}
							</button>
						)
					: undefined
			}
			className={cn(
				segmented
					? segmentedItemClass
					: "-mb-px h-[34px] flex-none gap-2 rounded-none border-0 border-b-2 border-transparent px-2.5 text-[14px] font-medium text-fg3 after:hidden hover:text-foreground data-active:border-foreground data-active:text-foreground dark:text-fg3 dark:data-active:border-foreground",
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
