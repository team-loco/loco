import { XIcon } from "lucide-react";

import {
	Dialog,
	DialogClose,
	DialogContent as DialogContentBase,
	DialogDescription as DialogDescriptionBase,
	DialogTitle as DialogTitleBase,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

const dialogWidths = {
	default: "w-[460px]",
	wide: "w-[680px]",
} as const;

const modalSurface =
	"max-h-[calc(100dvh-32px)] max-w-[calc(100%-32px)] gap-0 rounded-overlay border border-line bg-background p-0 text-base shadow-overlay ring-0 dark:border-line2";

const modalTitle = "text-[17px] leading-normal font-medium tracking-[-0.01em]";

function DialogContent({
	className,
	children,
	title,
	size = "default",
	showCloseButton = true,
	...props
}: Omit<React.ComponentProps<typeof DialogContentBase>, "title"> & {
	title?: React.ReactNode;
	size?: keyof typeof dialogWidths;
}) {
	return (
		<DialogContentBase
			showCloseButton={title === undefined && showCloseButton}
			className={cn(modalSurface, "flex flex-col sm:max-w-[calc(100%-32px)]", dialogWidths[size], className)}
			{...props}
		>
			{title !== undefined && (
				<div className="flex items-start gap-2 px-5 pt-[18px]">
					<DialogTitle className="flex-1">{title}</DialogTitle>
					{showCloseButton && (
						<DialogClose
							aria-label="Close"
							className="-mt-0.5 -mr-1.5 flex size-7 items-center justify-center rounded-lg text-fg3 hover:bg-panel2 hover:text-foreground"
						>
							<XIcon className="size-4" />
						</DialogClose>
					)}
				</div>
			)}
			{children}
		</DialogContentBase>
	);
}

function DialogBody({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="dialog-body"
			className={cn("flex min-h-0 flex-col gap-3 overflow-y-auto px-5 pt-2.5 pb-[18px]", className)}
			{...props}
		/>
	);
}

function DialogFooter({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div
			data-slot="dialog-footer"
			className={cn("flex shrink-0 justify-end gap-2 border-t border-line px-5 py-3.5", className)}
			{...props}
		/>
	);
}

function DialogTitle({ className, ...props }: React.ComponentProps<typeof DialogTitleBase>) {
	return <DialogTitleBase className={cn(modalTitle, className)} {...props} />;
}

function DialogDescription({ className, ...props }: React.ComponentProps<typeof DialogDescriptionBase>) {
	return <DialogDescriptionBase className={cn("text-[13.5px] text-fg2", className)} {...props} />;
}

export {
	Dialog,
	DialogBody,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogTitle,
	modalSurface,
	modalTitle,
};
