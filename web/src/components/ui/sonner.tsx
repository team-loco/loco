import { useTheme } from "next-themes";
import { Toaster as Sonner, type ToasterProps } from "sonner";

const Toaster = ({ ...props }: ToasterProps) => {
	const { theme = "system" } = useTheme();

	return (
		<Sonner
			theme={theme as ToasterProps["theme"]}
			// closeButton
			position="bottom-right"
			style={{ fontFamily: "inherit", overflowWrap: "anywhere" }}
			toastOptions={{
				unstyled: true,
				classNames: {
					toast:
						"bg-bg-default text-fg-default border-border-default border-2 font-heading rounded-base text-[13px] flex items-center gap-2.5 px-5 py-5 w-[356px] [&:has(button)]:justify-between",
					description: "text-fg-muted font-base",
					actionButton:
						"font-base border-2 text-[12px] h-6 px-2 bg-blue text-white border-border-default rounded-base shrink-0",
					cancelButton:
						"font-base border-2 text-[12px] h-6 px-2 bg-bg-subtle text-fg-default border-border-default rounded-base shrink-0",
					error: "bg-error-bg text-error-text border-error-border",
					warning: "bg-warning-bg text-warning-text border-warning-border",
					success: "bg-success-bg text-success-text border-success-border",
					loading:
						"[&[data-sonner-toast]_[data-icon]]:flex [&[data-sonner-toast]_[data-icon]]:size-4 [&[data-sonner-toast]_[data-icon]]:relative [&[data-sonner-toast]_[data-icon]]:justify-start [&[data-sonner-toast]_[data-icon]]:items-center [&[data-sonner-toast]_[data-icon]]:flex-shrink-0",
				},
			}}
			{...props}
		/>
	);
};

export { Toaster };
