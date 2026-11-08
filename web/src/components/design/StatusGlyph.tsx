import { cn } from "@/lib/utils";

type Glyph = "ok" | "spin" | "warn" | "err" | "off" | "draft";

const INK = "var(--bg)";
const STROKE = 1.6;

function glyphClass(glyph: Glyph): string {
	switch (glyph) {
		case "ok":
			return "text-ok-glyph";
		case "spin":
			return "animate-spin text-info motion-reduce:animate-none";
		case "warn":
			return "text-warn";
		case "err":
			return "text-err";
		case "off":
			return "text-fg4";
		case "draft":
			return "text-fg3";
	}
}

function GlyphParts({ glyph }: { glyph: Glyph }) {
	switch (glyph) {
		case "ok":
			return (
				<>
					<circle cx={7} cy={7} r={7} fill="currentColor" />
					<path
						d="M4.2 7.2l1.9 1.9 3.7-4"
						fill="none"
						stroke={INK}
						strokeWidth={STROKE}
						strokeLinecap="round"
						strokeLinejoin="round"
					/>
				</>
			);
		case "spin":
			return (
				<>
					<circle cx={7} cy={7} r={5.6} fill="none" stroke="currentColor" strokeOpacity={0.25} strokeWidth={2} />
					<path
						d="M7 1.4a5.6 5.6 0 0 1 5.6 5.6"
						fill="none"
						stroke="currentColor"
						strokeWidth={2}
						strokeLinecap="round"
					/>
				</>
			);
		case "warn":
			return (
				<>
					<circle cx={7} cy={7} r={7} fill="currentColor" />
					<path d="M7 3.7v4" stroke={INK} strokeWidth={STROKE} strokeLinecap="round" />
					<circle cx={7} cy={10.1} r={0.95} fill={INK} />
				</>
			);
		case "err":
			return (
				<>
					<circle cx={7} cy={7} r={7} fill="currentColor" />
					<path d="M4.9 4.9l4.2 4.2M9.1 4.9l-4.2 4.2" stroke={INK} strokeWidth={STROKE} strokeLinecap="round" />
				</>
			);
		case "off":
			return (
				<>
					<circle cx={7} cy={7} r={6.1} fill="none" stroke="currentColor" strokeWidth={STROKE} />
					<path d="M4.6 7h4.8" stroke="currentColor" strokeWidth={STROKE} strokeLinecap="round" />
				</>
			);
		case "draft":
			return (
				<circle cx={7} cy={7} r={6.1} fill="none" stroke="currentColor" strokeWidth={1.5} strokeDasharray="2.2 2" />
			);
	}
}

function StatusGlyph({ glyph, className }: { glyph: Glyph; className?: string | undefined }) {
	return (
		<svg
			width={14}
			height={14}
			viewBox="0 0 14 14"
			aria-hidden="true"
			className={cn("block shrink-0", glyphClass(glyph), className)}
		>
			<GlyphParts glyph={glyph} />
		</svg>
	);
}

function StatusDot({ pulse = false, className }: { pulse?: boolean | undefined; className?: string | undefined }) {
	return (
		<span
			aria-hidden="true"
			className={cn(
				"relative inline-block size-[7px] shrink-0 rounded-full bg-current",
				pulse &&
					"after:absolute after:-inset-[3px] after:animate-ring after:rounded-full after:border-[1.5px] after:border-current motion-reduce:after:animate-none",
				className,
			)}
		/>
	);
}

export { StatusDot, StatusGlyph, type Glyph };
