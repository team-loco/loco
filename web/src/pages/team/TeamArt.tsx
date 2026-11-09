import type { CSSProperties } from "react";

const CYCLE = "4.8s ease-out infinite both";
const INK = "color-mix(in oklch, var(--fg) 42%, var(--bg))";
const FAINT = "color-mix(in oklch, var(--fg) 22%, var(--bg))";
const SEAT_DELAYS = ["0.9s", "1.8s"];
const ROW_Y = [37, 59, 81];
const NAME_WIDTHS = [44, 38, 48];
const AVATAR_X = 150;
const AVATAR_R = 6;
const TEXT_X = 162;

function seatStyle(delay: string): CSSProperties {
	return {
		animation: `team-seat ${CYCLE}`,
		animationDelay: delay,
		transformBox: "fill-box",
		transformOrigin: "center",
	};
}

export function TeamArt() {
	return (
		<svg viewBox="128 12 112 96" className="pe-art block w-full max-w-[200px] overflow-visible py-2" aria-hidden="true">
			<rect x={134} y={18} width={98} height={84} rx={7} fill="var(--bg)" stroke="var(--line2)" strokeWidth={1} />
			{ROW_Y.map((y, i) => {
				const delay = SEAT_DELAYS[i - 1];
				return (
					<g key={y}>
						{delay === undefined ? (
							<circle cx={AVATAR_X} cy={y} r={AVATAR_R} fill={INK} />
						) : (
							<>
								<circle
									cx={AVATAR_X}
									cy={y}
									r={AVATAR_R}
									fill="none"
									stroke="var(--info)"
									strokeWidth={1.2}
									strokeDasharray="2 2"
								/>
								<circle cx={AVATAR_X} cy={y} r={AVATAR_R} fill={INK} style={seatStyle(delay)} />
							</>
						)}
						<rect x={TEXT_X} y={y - 4} width={NAME_WIDTHS[i]} height={3.5} rx={1.75} fill={INK} />
						<rect x={TEXT_X} y={y + 2} width={(NAME_WIDTHS[i] ?? 0) * 0.7} height={2.5} rx={1.25} fill={FAINT} />
					</g>
				);
			})}
		</svg>
	);
}
