import { cn } from "@/lib/utils";

function LocoLogo({ className, title = "Loco" }: { className?: string | undefined; title?: string | undefined }) {
	// A mask lets each colour theme paint the shared logo file with its own --logo; the aspect ratio is the file's viewBox.
	return (
		<span
			role="img"
			aria-label={title}
			className={cn("inline-block aspect-[995/475] bg-[var(--logo)]", className)}
			style={{ mask: "url(/logo.svg) center / contain no-repeat" }}
		/>
	);
}

export { LocoLogo };
