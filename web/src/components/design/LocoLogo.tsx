import { useEffect, useState } from "react"

import { cn } from "@/lib/utils"

import { LOGO_DRAW_SECONDS, LOGO_STROKES, LOGO_VIEWBOX } from "./logo-strokes"

type LogoMotion = "none" | "once" | "loop"

const drawnThisLoad = new Set<string>()

const HOLD_SECONDS = 0.9
const FADE_SECONDS = 0.35

function LocoLogo({
	motion = "none",
	speed = 1,
	drawKey = "logo",
	className,
	title = "Loco",
}: {
	motion?: LogoMotion | undefined
	drawKey?: string | undefined
	speed?: number | undefined
	className?: string | undefined
	title?: string | undefined
}) {
	const [cycle, setCycle] = useState(0)
	const [firstDraw] = useState(() => motion === "once" && !drawnThisLoad.has(drawKey))
	useEffect(() => {
		if (motion === "once") drawnThisLoad.add(drawKey)
	}, [motion, drawKey])
	const draw = LOGO_DRAW_SECONDS / speed
	const total = draw + HOLD_SECONDS + FADE_SECONDS
	const animated = motion === "loop" || firstDraw

	return (
		<span
			key={cycle}
			className={cn("loco-logo inline-block", animated && "loco-logo-animated", className)}
			style={
				motion === "loop"
					? { animation: `loco-logo-cycle ${total}s linear` }
					: undefined
			}
			onAnimationEnd={(e) => {
				if (motion === "loop" && e.target === e.currentTarget) setCycle((c) => c + 1)
			}}
		>
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
		</span>
	)
}

export { LocoLogo, type LogoMotion }
