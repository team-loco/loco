import "./WebhooksArt.css";

export function WebhooksArt() {
	return (
		<svg
			viewBox="0 0 300 120"
			width={300}
			height={120}
			aria-hidden="true"
			className="es-wh block max-w-full overflow-visible"
		>
			<rect x={8} y={38} width={122} height={44} rx={7} fill="var(--raise)" stroke="var(--line2)" />
			<circle cx={24} cy={52} r={3.5} fill="var(--ok-glyph)" />
			<text x={33} y={55.5} fontFamily="var(--font-mono)" fontSize={9.5} fill="var(--fg)">
				build.succeeded
			</text>
			<rect x={20} y={63} width={54} height={5} rx={2.5} fill="var(--line2)" />
			<rect x={78} y={63} width={30} height={5} rx={2.5} fill="var(--line)" />
			<path d="M130 60 H188" stroke="var(--line2)" strokeWidth={1.5} strokeDasharray="3 4" fill="none" />
			<text x={146} y={52} fontFamily="var(--font-mono)" fontSize={8.5} fill="var(--fg3)">
				POST
			</text>
			<circle className="es-wh-dot" r={4} fill="var(--fg)" />
			<rect x={188} y={30} width={104} height={60} rx={7} fill="var(--raise)" stroke="var(--line2)" />
			<rect x={188} y={30} width={104} height={18} rx={7} fill="var(--panel2)" />
			<rect x={188} y={41} width={104} height={7} fill="var(--panel2)" />
			{[198, 205, 212].map((cx) => (
				<circle key={cx} cx={cx} cy={39} r={2.2} fill="var(--line2)" />
			))}
			<text x={198} y={63} fontFamily="var(--font-mono)" fontSize={9} fill="var(--fg2)">
				/hooks
			</text>
			<g className="es-wh-ok">
				<rect x={198} y={70} width={42} height={13} rx={3.5} fill="color-mix(in oklch, var(--ok) 16%, transparent)" />
				<text x={203} y={79.5} fontFamily="var(--font-mono)" fontSize={9} fill="var(--ok)">
					200 OK
				</text>
				<text x={246} y={79.5} fontFamily="var(--font-mono)" fontSize={8.5} fill="var(--fg3)">
					41ms
				</text>
			</g>
		</svg>
	);
}
