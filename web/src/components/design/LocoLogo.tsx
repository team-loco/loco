import { cn } from "@/lib/utils";

function LocoLogo({
	className,
	title = "Loco",
	src = "/logo.svg",
}: {
	className?: string | undefined;
	title?: string | undefined;
	src?: string | undefined;
}) {
	// A mask lets each colour theme paint the shared logo file with its own --logo; the aspect ratio is the file's viewBox.
	return (
		<span
			role="img"
			aria-label={title}
			className={cn("inline-block aspect-[995/475] bg-[var(--logo)]", className)}
			style={{ mask: `url("${src}") center / contain no-repeat` }}
		/>
	);
}

export { LocoLogo };
