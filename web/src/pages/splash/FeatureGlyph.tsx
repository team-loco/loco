import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export type GlyphKind = "tls" | "quic" | "roll" | "scale" | "obs" | "token";

const BASE = "fill-none stroke-line2 [stroke-width:1.2]";
const ACCENT = "fill-none stroke-primary [stroke-width:1.4]";

function Label({
	x,
	y,
	accent,
	center,
	children,
}: {
	x: number;
	y: number;
	accent?: string | undefined;
	center?: boolean | undefined;
	children: string;
}) {
	return (
		<text
			x={x}
			y={y}
			textAnchor={center === true ? "middle" : undefined}
			className={cn("font-mono text-[8.5px]", accent ?? "fill-fg3")}
		>
			{children}
		</text>
	);
}

function TlsGlyph() {
	return (
		<>
			<rect x={2} y={18} width={38} height={20} rx={4} className={BASE} />
			<rect x={58} y={18} width={38} height={20} rx={4} className={BASE} />
			<rect x={114} y={18} width={38} height={20} rx={4} className={ACCENT} />
			<path d="M40,28 L58,28 M96,28 L114,28" className={BASE} />
			<Label x={21} y={31} center>
				root
			</Label>
			<Label x={77} y={31} center>
				inter
			</Label>
			<Label x={133} y={31} accent="fill-primary" center>
				leaf
			</Label>
			<Label x={84} y={50}>
				api.onloco.app
			</Label>
		</>
	);
}

function QuicGlyph() {
	return (
		<>
			<path d="M10,6 L10,50 M146,6 L146,50" className={BASE} />
			<path d="M10,14 L146,24 M146,30 L10,40" className={ACCENT} />
			<path d="M10,40 L146,50" strokeDasharray="3 3" className={BASE} />
			<Label x={16} y={10}>
				client
			</Label>
			<Label x={112} y={10}>
				envoy
			</Label>
			<Label x={52} y={54}>
				1-RTT handshake
			</Label>
		</>
	);
}

const ROLL_PODS = [0, 1, 2, 3, 4, 5] as const;

function RollGlyph() {
	return (
		<>
			{ROLL_PODS.map((i) => (
				<rect
					key={i}
					x={6 + i * 24}
					y={16}
					width={16}
					height={16}
					rx={3.5}
					strokeWidth={1.3}
					opacity={i === 2 ? 0.45 : 1}
					className={i < 3 ? "fill-bg3 stroke-line2" : "fill-ok-bg stroke-ok-fg"}
				/>
			))}
			<path d="M78,40 L92,40" className={ACCENT} />
			<Label x={6} y={50}>
				v1 drain
			</Label>
			<Label x={98} y={50} accent="fill-ok-fg">
				v2 ready
			</Label>
		</>
	);
}

const SCALE_BARS = [8, 14, 22, 30, 36, 28, 20] as const;

function ScaleGlyph() {
	return (
		<>
			<path d="M4,46 L152,46" className={BASE} />
			{SCALE_BARS.map((v, i) => (
				<rect
					key={i}
					x={8 + i * 21}
					y={46 - v}
					width={13}
					height={v}
					rx={2}
					strokeWidth={1}
					className={v > 26 ? "fill-primary/20 stroke-primary" : "fill-bg3 stroke-line2"}
				/>
			))}
			<path d="M4,20 L152,20" strokeDasharray="4 3" className={ACCENT} />
			<Label x={118} y={16} accent="fill-primary">
				cpu 70%
			</Label>
		</>
	);
}

const OBS_LOG_LINES = [
	[38, 120],
	[46, 90],
	[54, 140],
] as const;

function ObsGlyph() {
	return (
		<>
			<path d="M4,30 L24,24 L40,28 L58,12 L76,22 L96,18 L116,26 L136,8 L152,14" className={ACCENT} />
			{OBS_LOG_LINES.map(([y, end]) => (
				<path key={y} d={`M4,${y} L${end},${y}`} className={BASE} />
			))}
		</>
	);
}

function TokenGlyph() {
	return (
		<>
			<circle cx={20} cy={28} r={5} className={ACCENT} />
			<path d="M25,28 L60,12 M25,28 L60,44" className={BASE} />
			<rect x={60} y={4} width={64} height={16} rx={4} className={ACCENT} />
			<rect x={60} y={36} width={64} height={16} rx={4} className={BASE} />
			<Label x={92} y={15} accent="fill-primary" center>
				api:write
			</Label>
			<Label x={92} y={47} center>
				web:—
			</Label>
			<Label x={10} y={46}>
				ci
			</Label>
		</>
	);
}

function glyphFor(kind: GlyphKind): ReactNode {
	switch (kind) {
		case "tls":
			return <TlsGlyph />;
		case "quic":
			return <QuicGlyph />;
		case "roll":
			return <RollGlyph />;
		case "scale":
			return <ScaleGlyph />;
		case "obs":
			return <ObsGlyph />;
		case "token":
			return <TokenGlyph />;
	}
}

export function FeatureGlyph({ kind }: { kind: GlyphKind }) {
	return (
		<svg width={160} height={56} viewBox="0 0 160 56" aria-hidden="true" className="block">
			{glyphFor(kind)}
		</svg>
	);
}
