import { CircleAlertIcon, CircleCheckIcon } from "lucide-react";

import { Toaster as ToasterBase } from "@/components/ui/sonner";

const card = {
	"--normal-bg": "var(--bg)",
	"--normal-text": "var(--fg)",
	"--normal-border": "var(--line)",
	"--border-radius": "10px",
	"--width": "360px",
} as React.CSSProperties;

function Toaster() {
	return (
		<ToasterBase
			position="bottom-right"
			offset={20}
			mobileOffset={12}
			gap={8}
			closeButton
			style={card}
			icons={{
				success: <CircleCheckIcon className="size-4 text-ok" />,
				error: <CircleAlertIcon className="size-4 text-err" />,
				loading: (
					<span className="size-[13px] animate-spin rounded-full border-[1.6px] border-[color-mix(in_oklch,var(--fg)_18%,transparent)] border-t-fg2 motion-reduce:[animation-duration:2.4s]" />
				),
			}}
			toastOptions={{
				classNames: {
					toast: "cn-toast group/toast min-h-[46px] gap-2.5! py-2! pr-2! pl-3.5! text-[13px]! shadow-toast!",
					icon: "m-0! text-fg2",
					title: "font-normal! leading-[1.35]!",
					description: "text-[12.5px]! leading-[1.4]! text-fg2!",
					actionButton:
						"h-7! gap-1.5 rounded-[7px]! bg-primary! px-[11px]! text-[12.5px]! font-normal! text-primary-foreground! hover:opacity-88 [&_svg]:size-[13px] [&_svg]:transition-transform [&_svg]:duration-[260ms] hover:[&_svg]:rotate-90",
					closeButton:
						"static! order-last size-[26px]! shrink-0 transform-none! rounded-md! border-0! bg-transparent! text-fg3! opacity-0 group-hover/toast:opacity-100 focus-visible:opacity-100 hover:bg-panel! hover:text-foreground!",
				},
			}}
		/>
	);
}

export { Toaster };
