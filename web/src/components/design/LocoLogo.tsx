import { useEffect, useEffectEvent, useRef, useState } from "react"

import { cn } from "@/lib/utils"

import { LOGO_DRAW_SECONDS, LOGO_STROKES, LOGO_VIEWBOX } from "./logo-strokes"

type LogoMotion = "none" | "once" | "loop"

const drawnThisLoad = new Set<string>()

const RUSH_MS = 300

function targets(a: Animation, el: Element): boolean {
	return a.effect instanceof KeyframeEffect && a.effect.target === el
}

function msOf(value: CSSNumberish | null | undefined): number {
	return typeof value === "number" ? value : 0
}

const HOLD_SECONDS = 0.9
const FADE_SECONDS = 0.35

function LocoLogo({
	motion = "none",
	speed = 1,
	drawKey = "logo",
	finishing = false,
	onFinished,
	className,
	title = "Loco",
}: {
	motion?: LogoMotion | undefined
	drawKey?: string | undefined
	finishing?: boolean | undefined
	onFinished?: (() => void) | undefined
	speed?: number | undefined
	className?: string | undefined
	title?: string | undefined
}) {
	const [cycle, setCycle] = useState(0)
	const [firstDraw] = useState(() => motion === "once" && !drawnThisLoad.has(drawKey))
	useEffect(() => {
		if (motion === "once") drawnThisLoad.add(drawKey)
	}, [motion, drawKey])
	const ref = useRef<HTMLSpanElement>(null)
	const wasFinishing = useRef(false)
	const notifyFinished = useEffectEvent(() => {
		onFinished?.()
	})
	useEffect(() => {
		const el = ref.current
		if (!finishing) {
			if (wasFinishing.current) {
				wasFinishing.current = false
				setCycle((c) => c + 1)
			}
			return
		}
		wasFinishing.current = true
		if (!el) {
			notifyFinished()
			return
		}
		const all = el.getAnimations({ subtree: true })
		for (const a of all) {
			if (targets(a, el)) a.cancel()
		}
		const draws = all.filter((a) => !targets(a, el) && a.playState !== "finished")
		let remaining = 0
		for (const a of draws) {
			const left = msOf(a.effect?.getComputedTiming().endTime) - msOf(a.currentTime)
			if (left > remaining) remaining = left
		}
		const rate = Math.max(1, remaining / RUSH_MS)
		for (const a of draws) a.updatePlaybackRate(rate)
		let settled = false
		const finish = () => {
			if (settled) return
			settled = true
			notifyFinished()
		}
		const safety = window.setTimeout(finish, RUSH_MS + 700)
		void Promise.allSettled(draws.map(async (a) => await a.finished)).then(finish)
		return () => {
			settled = true
			window.clearTimeout(safety)
		}
	}, [finishing])
	const draw = LOGO_DRAW_SECONDS / speed
	const total = draw + HOLD_SECONDS + FADE_SECONDS
	const animated = motion === "loop" || firstDraw

	return (
		<span
			ref={ref}
			key={cycle}
			className={cn("loco-logo inline-block", animated && "loco-logo-animated", className)}
			style={
				motion === "loop" && !finishing
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
