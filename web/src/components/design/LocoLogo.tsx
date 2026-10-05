import { useState } from "react"

import { cn } from "@/lib/utils"

import { LOGO_DRAW_SECONDS, LOGO_STROKES, LOGO_VIEWBOX } from "./logo-strokes"

type LogoMotion = "none" | "once" | "loop"

const HOLD_SECONDS = 0.9
const FADE_SECONDS = 0.35

function LocoLogo({
	motion = "none",
	speed = 1,
	className,
	title = "Loco",
}: {
	motion?: LogoMotion | undefined
	speed?: number | undefined
	className?: string | undefined
	title?: string | undefined
}) {
	const [cycle, setCycle] = useState(0)
	const draw = LOGO_DRAW_SECONDS / speed
	const total = draw + HOLD_SECONDS + FADE_SECONDS
	const animated = motion !== "none"

	return (
		<svg
			key={cycle}
			viewBox={LOGO_VIEWBOX}
			role="img"
			aria-label={title}
			fill="none"
			stroke="var(--logo)"
			strokeLinecap="round"
			strokeLinejoin="round"
			className={cn("loco-logo h-auto", animated && "loco-logo-animated", className)}
			style={
				motion === "loop"
					? { animation: `loco-logo-cycle ${total}s linear` }
					: undefined
			}
			onAnimationEnd={(e) => {
				if (motion === "loop" && e.target === e.currentTarget) setCycle((c) => c + 1)
			}}
		>
			<title>{title}</title>
			{LOGO_STROKES.map(([d, width, start, duration]) => (
				<path
					key={d}
					d={d}
					strokeWidth={width}
					pathLength={1}
					style={
						animated
							? { animation: `loco-logo-draw ${duration / speed}s linear ${start / speed}s forwards` }
							: undefined
					}
				/>
			))}
		</svg>
	)
}

export { LocoLogo, type LogoMotion }
