import type { ReactNode } from "react";

export const ART_STRONG = "color-mix(in oklab, var(--fg) 42%, var(--bg))";
export const ART_SOFT = "color-mix(in oklab, var(--fg) 22%, var(--bg))";

const PODS: [number, number][] = [
	[4, 4],
	[11, 4],
	[4, 11],
	[11, 11],
];

export function ClusterArt({ children }: { children: ReactNode }) {
	return (
		<svg
			viewBox="14 10 226 100"
			width={300}
			height={133}
			aria-hidden="true"
			className="block max-w-full overflow-visible"
		>
			<path d="M24 60 L58 77 L58 85 L24 68 Z" fill="var(--panel2)" stroke={ART_STRONG} strokeLinejoin="round" />
			<path
				d="M58 77 L92 60 L92 68 L58 85 Z"
				fill="color-mix(in oklab, var(--panel2) 70%, var(--line2))"
				stroke={ART_STRONG}
				strokeLinejoin="round"
			/>
			<path d="M58 43 L92 60 L58 77 L24 60 Z" fill="var(--bg)" stroke={ART_STRONG} strokeLinejoin="round" />
			<g transform="matrix(1.7 0.85 -1.7 0.85 58 43)">
				{PODS.map(([x, y]) => (
					<rect key={`${String(x)}-${String(y)}`} x={x} y={y} width={5} height={5} rx={0.7} fill={ART_STRONG} />
				))}
			</g>
			<path d="M92 62 C112 62 112 60 134 60" fill="none" stroke={ART_SOFT} strokeDasharray="2 2.5" />
			<rect x={134} y={18} width={98} height={84} rx={7} fill="var(--bg)" stroke="var(--line2)" />
			{children}
		</svg>
	);
}
