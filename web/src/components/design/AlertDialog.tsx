import {
	AlertDialog,
	AlertDialogCancel as AlertDialogCancelBase,
	AlertDialogContent as AlertDialogContentBase,
	AlertDialogDescription as AlertDialogDescriptionBase,
	AlertDialogFooter as AlertDialogFooterBase,
	AlertDialogHeader as AlertDialogHeaderBase,
	AlertDialogTitle as AlertDialogTitleBase,
} from "@/components/ui/alert-dialog";
import { cn } from "@/lib/utils";

import { Button, type ButtonVariant } from "./Button";
import { modalSurface, modalTitle } from "./Dialog";

function AlertDialogContent({ className, ...props }: React.ComponentProps<typeof AlertDialogContentBase>) {
	return (
		<AlertDialogContentBase
			className={cn(
				modalSurface,
				"w-[460px] data-[size=default]:max-w-[calc(100%-32px)] data-[size=default]:sm:max-w-[calc(100%-32px)] data-[size=sm]:max-w-[calc(100%-32px)]",
				className,
			)}
			{...props}
		/>
	);
}

function AlertDialogHeader({ className, ...props }: React.ComponentProps<typeof AlertDialogHeaderBase>) {
	return (
		<AlertDialogHeaderBase
			className={cn("flex flex-col items-start gap-2.5 px-5 py-[18px] text-left sm:text-left", className)}
			{...props}
		/>
	);
}

function AlertDialogTitle({ className, ...props }: React.ComponentProps<typeof AlertDialogTitleBase>) {
	return <AlertDialogTitleBase className={cn(modalTitle, className)} {...props} />;
}

function AlertDialogDescription({ className, ...props }: React.ComponentProps<typeof AlertDialogDescriptionBase>) {
	return <AlertDialogDescriptionBase className={cn("text-[13.5px] text-fg2", className)} {...props} />;
}

function AlertDialogFooter({ className, ...props }: React.ComponentProps<typeof AlertDialogFooterBase>) {
	return (
		<AlertDialogFooterBase
			className={cn(
				"m-0 flex-row justify-end gap-2 rounded-none border-t border-line bg-transparent px-5 py-3.5 sm:flex-row",
				className,
			)}
			{...props}
		/>
	);
}

function AlertDialogCancel({
	children,
	...props
}: Omit<React.ComponentProps<typeof AlertDialogCancelBase>, "variant" | "size" | "render">) {
	return (
		<AlertDialogCancelBase render={<Button variant="outline" />} {...props}>
			{children}
		</AlertDialogCancelBase>
	);
}

function AlertDialogAction({
	variant = "destructive",
	...props
}: Omit<React.ComponentProps<typeof Button>, "variant"> & { variant?: ButtonVariant | undefined }) {
	return <Button data-slot="alert-dialog-action" variant={variant} {...props} />;
}

export {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
};
