import {
	Sheet,
	SheetContent as SheetContentBase,
	SheetDescription as SheetDescriptionBase,
	SheetTitle as SheetTitleBase,
} from "@/components/ui/sheet";
import { cn } from "@/lib/utils";

const sheetWidths = {
	default: "data-[side=right]:w-[min(440px,calc(100vw-16px))]",
	wide: "data-[side=right]:w-[min(520px,calc(100vw-16px))]",
} as const;

function SheetContent({
	className,
	side = "right",
	size = "default",
	...props
}: React.ComponentProps<typeof SheetContentBase> & { size?: keyof typeof sheetWidths }) {
	return (
		<SheetContentBase
			side={side}
			className={cn(
				"gap-0 border border-line bg-background text-base shadow-overlay duration-[320ms] ease-[cubic-bezier(0.2,0.8,0.2,1)] motion-reduce:transition-none dark:border-line2 data-[side=right]:inset-y-2 data-[side=right]:right-2 data-[side=right]:h-auto data-[side=right]:max-w-none data-[side=right]:rounded-[12px] data-[side=right]:sm:max-w-none data-[side=right]:data-ending-style:translate-x-[calc(100%+16px)] data-[side=right]:data-starting-style:translate-x-[calc(100%+16px)] [&>[data-slot=sheet-close]]:top-[18px] [&>[data-slot=sheet-close]]:right-4 [&>[data-slot=sheet-close]]:text-fg3",
				sheetWidths[size],
				className,
			)}
			{...props}
		/>
	);
}

function SheetHeader({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="sheet-header"
			className={cn(
				"relative flex min-h-16 shrink-0 items-center gap-3 border-b border-line py-3 pr-14 pl-6",
				className,
			)}
			{...props}
		/>
	);
}

function SheetBody({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="sheet-body"
			className={cn("flex min-h-0 flex-1 flex-col gap-[18px] overflow-y-auto px-6 py-5", className)}
			{...props}
		/>
	);
}

function SheetFooter({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="sheet-footer"
			className={cn("flex shrink-0 justify-end gap-2 border-t border-line px-6 py-3.5", className)}
			{...props}
		/>
	);
}

function SheetTitle({ className, ...props }: React.ComponentProps<typeof SheetTitleBase>) {
	return <SheetTitleBase className={cn("text-[20px] font-normal tracking-[-0.02em]", className)} {...props} />;
}

function SheetDescription({ className, ...props }: React.ComponentProps<typeof SheetDescriptionBase>) {
	return <SheetDescriptionBase className={cn("text-sm text-fg3", className)} {...props} />;
}

export { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle };
