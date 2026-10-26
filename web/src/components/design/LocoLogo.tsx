import { cn } from "@/lib/utils";

import { LOGO_STROKES, LOGO_VIEWBOX } from "./logo-strokes";

function LocoLogo({ className, title = "Loco" }: { className?: string | undefined; title?: string | undefined }) {
	return (
		<span className={cn("loco-logo inline-block", className)}>
			<svg
				viewBox={LOGO_VIEWBOX}
				role="img"
				aria-label={title}
				fill="none"
				stroke="var(--logo)"
				strokeLinecap="round"
				strokeLinejoin="round"
				className="block h-auto w-full"
			>
				<title>{title}</title>
				{LOGO_STROKES.map(([d, width]) => (
					<path key={d} d={d} strokeWidth={width} />
				))}
			</svg>
		</span>
	);
}

export { LocoLogo };
