import { Toaster as ToasterBase } from "@/components/ui/sonner";

const inverse = {
	"--normal-bg": "var(--btn)",
	"--normal-text": "var(--btn-fg)",
	"--normal-border": "transparent",
	"--border-radius": "8px",
} as React.CSSProperties;

function Toaster() {
	return (
		<ToasterBase
			position="bottom-center"
			style={inverse}
			toastOptions={{
				classNames: {
					toast: "cn-toast gap-2.5! px-3.5! py-2.5! text-[13px]! shadow-toast!",
					icon: "opacity-70",
					description: "text-primary-foreground/70!",
				},
			}}
		/>
	);
}

export { Toaster };
