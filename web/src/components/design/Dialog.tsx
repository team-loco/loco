import { XIcon } from "lucide-react"

import {
	Dialog,
	DialogClose,
	DialogContent as DialogContentBase,
	DialogDescription as DialogDescriptionBase,
	DialogHeader as DialogHeaderBase,
	DialogPortal,
	DialogTitle,
	DialogTrigger,
} from "@/components/ui/dialog"
import { cn } from "@/lib/utils"

function DialogContent({
	className,
	children,
	icon,
	title,
	showCloseButton = true,
	...props
}: Omit<React.ComponentProps<typeof DialogContentBase>, "title"> & {
	icon?: React.ReactNode
	title?: React.ReactNode
}) {
	return (
		<DialogContentBase
			showCloseButton={title === undefined && showCloseButton}
			className={cn(
				"top-[14vh] flex max-h-[80vh] w-[440px] max-w-[calc(100%-32px)] translate-y-0 flex-col gap-0 rounded-xl border border-line bg-background p-0 text-base shadow-popover ring-0 sm:max-w-[calc(100%-32px)]",
				className
			)}
			{...props}
		>
			{title !== undefined && (
				<div className="flex items-center gap-2 border-b border-line px-4 py-3.5">
					{icon !== undefined && <span className="flex text-fg3 [&_svg]:size-[15px]">{icon}</span>}
					<DialogTitle className="flex-1 text-lg leading-normal font-semibold">{title}</DialogTitle>
					{showCloseButton && (
						<DialogClose
							aria-label="Close"
							className="flex size-7 items-center justify-center rounded-sm text-fg3 hover:bg-bg3 hover:text-foreground"
						>
							<XIcon className="size-4" />
						</DialogClose>
					)}
				</div>
			)}
			{children}
		</DialogContentBase>
	)
}

function DialogBody({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="dialog-body"
			className={cn("flex min-h-0 flex-col gap-3.5 overflow-y-auto p-4", className)}
			{...props}
		/>
	)
}

function DialogFooter({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="dialog-footer"
			className={cn("flex justify-end gap-2 border-t border-line px-4 py-3", className)}
			{...props}
		/>
	)
}

function DialogHeader({ className, ...props }: React.ComponentProps<typeof DialogHeaderBase>) {
	return <DialogHeaderBase className={cn("gap-1 border-b border-line px-4 py-3.5", className)} {...props} />
}

function DialogDescription({ className, ...props }: React.ComponentProps<typeof DialogDescriptionBase>) {
	return <DialogDescriptionBase className={cn("text-base text-fg2", className)} {...props} />
}

export {
	Dialog,
	DialogBody,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogPortal,
	DialogTitle,
	DialogTrigger,
}
