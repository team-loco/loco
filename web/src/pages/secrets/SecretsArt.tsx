const STRONG = "color-mix(in oklch, var(--fg) 42%, var(--bg))";
const SOFT = "color-mix(in oklch, var(--fg) 22%, var(--bg))";
const ROWS = [53, 75, 97];

export function SecretsArt({ environment }: { environment: string }) {
	return (
		<svg
			viewBox="0 0 356 124"
			width={356}
			height={124}
			aria-hidden="true"
			className="block max-w-full overflow-visible"
		>
			<rect x={6} y={10} width={150} height={104} rx={8} fill="var(--bg)" stroke={STRONG} />
			<text x={18} y={29} fontFamily="var(--font-mono)" fontSize={9.5} fill="var(--fg3)">
				{environment}
			</text>
			<line x1={6} y1={38} x2={156} y2={38} stroke={SOFT} />
			<g fontFamily="var(--font-mono)" fontSize={9} fill="var(--fg2)">
				<text x={18} y={56}>
					DATABASE_URL
				</text>
				<text x={18} y={78}>
					STRIPE_KEY
				</text>
				<text x={18} y={100}>
					SESSION_SECRET
				</text>
			</g>
			<g fill={STRONG}>
				{ROWS.flatMap((cy) => [118, 125, 132, 139].map((cx) => <circle key={`${cx}-${cy}`} cx={cx} cy={cy} r={1.6} />))}
			</g>
			<rect x={11} y={44} width={140} height={17} rx={4} fill="none" stroke="var(--fg2)" />
			<path d="M151 52.5 C178 52.5 178 52.5 192 52.5" fill="none" stroke="var(--fg2)" strokeDasharray="3 3" />
			<circle cx={192} cy={52.5} r={2.4} fill="var(--fg2)" />
			<rect x={192} y={22} width={158} height={62} rx={8} fill="var(--bg)" stroke={STRONG} />
			<text x={204} y={41} fontFamily="var(--font-mono)" fontSize={9.5} fill="var(--fg)">
				api
			</text>
			<line x1={192} y1={49} x2={350} y2={49} stroke={SOFT} />
			<text x={204} y={64} fontFamily="var(--font-mono)" fontSize={8.5} fill="var(--fg3)">
				DATABASE_URL
			</text>
			<text x={204} y={77} fontFamily="var(--font-mono)" fontSize={8.5} fill="var(--fg2)">
				{"${{ secrets.DATABASE_URL }}"}
			</text>
		</svg>
	);
}
