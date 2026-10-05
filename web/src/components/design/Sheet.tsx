import {
	Sheet,
	SheetClose,
	SheetContent as SheetContentBase,
	SheetDescription as SheetDescriptionBase,
	SheetTitle as SheetTitleBase,
	SheetTrigger,
} from "@/components/ui/sheet"
import { cn } from "@/lib/utils"

function SheetContent({ className, side = "right", ...props }: React.ComponentProps<typeof SheetContentBase>) {
	return (
		<SheetContentBase
			side={side}
			className={cn(
				"gap-0 border-line bg-background text-base shadow-drawer duration-[380ms] ease-[cubic-bezier(0.32,0.72,0,1)] data-[side=left]:w-[460px] data-[side=left]:max-w-[90vw] data-[side=left]:sm:max-w-[90vw] data-[side=right]:w-[460px] data-[side=right]:max-w-[90vw] data-[side=right]:sm:max-w-[90vw] [&>[data-slot=sheet-close]]:top-3.5 [&>[data-slot=sheet-close]]:right-3.5 [&>[data-slot=sheet-close]]:text-fg3",
				className
			)}
			{...props}
		/>
	)
}

function SheetHeader({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="sheet-header"
			className={cn("relative flex items-center gap-3 border-b border-line px-6 pt-6 pb-5", className)}
			{...props}
		/>
	)
}

function SheetBody({ className, ...props }: React.ComponentProps<"div">) {
	return <div data-slot="sheet-body" className={cn("min-h-0 flex-1 overflow-y-auto", className)} {...props} />
}

function SheetSection({
	className,
	title,
	children,
	...props
}: Omit<React.ComponentProps<"section">, "title"> & { title?: React.ReactNode }) {
	return (
		<section
			data-slot="sheet-section"
			className={cn("flex flex-col gap-3 border-b border-line px-6 py-5", className)}
			{...props}
		>
			{title !== undefined && <span className="font-semibold">{title}</span>}
			{children}
		</section>
	)
}

function SheetFooter({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="sheet-footer"
			className={cn("flex flex-col gap-2.5 border-t border-line px-6 pt-5 pb-6", className)}
			{...props}
		/>
	)
}

function SheetTitle({ className, ...props }: React.ComponentProps<typeof SheetTitleBase>) {
	return <SheetTitleBase className={cn("text-xl font-semibold", className)} {...props} />
}

function SheetDescription({ className, ...props }: React.ComponentProps<typeof SheetDescriptionBase>) {
	return <SheetDescriptionBase className={cn("text-sm text-fg3", className)} {...props} />
}

export {
	Sheet,
	SheetBody,
	SheetClose,
	SheetContent,
	SheetDescription,
	SheetFooter,
	SheetHeader,
	SheetSection,
	SheetTitle,
	SheetTrigger,
}
