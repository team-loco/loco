import { ArrowUpRightIcon, PlusIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";
import { CopyButton } from "@/components/design/CopyButton";
import { EmptyHero } from "@/components/design/EmptyHero";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { newResourcePath } from "@/lib/routes";
import { DOCS_GET_STARTED } from "@/pages/splash/links";

import "./EmptyWorkspace.css";
import { InlineCode } from "@/components/design/InlineCode";

const COMMANDS = ["loco login", "loco deploy"];
const TILE_STROKE = "color-mix(in oklch, var(--fg) 42%, var(--bg))";
const TOP_STROKE = "color-mix(in oklch, var(--info) 70%, var(--bg))";
const SIDE_FILL = "color-mix(in oklch, var(--panel2) 70%, var(--line2))";
const ISO = (y: number) => `matrix(2 1 -2 1 60 ${y.toString()})`;
const HATCH = [4, 6.5, 9, 11.5, 14, 16.5];
const FLOOR = Array.from({ length: 13 }, (_, i) => i * 4 - 14);
const PODS = [
	[4, 4],
	[10.5, 4],
	[4, 10.5],
	[10.5, 10.5],
];

function Tile({
	y,
	stroke,
	top,
	className,
	children,
}: {
	y: number;
	stroke: string;
	top: string;
	className: string;
	children?: ReactNode;
}) {
	const line = { stroke, strokeWidth: 1, strokeLinejoin: "round" as const };
	return (
		<g className={`stack-tile ${className}`}>
			<path
				d={`M20 ${(y + 20).toString()} L60 ${(y + 40).toString()} L60 ${(y + 46).toString()} L20 ${(y + 26).toString()} Z`}
				fill="var(--panel2)"
				{...line}
			/>
			<path
				d={`M60 ${(y + 40).toString()} L100 ${(y + 20).toString()} L100 ${(y + 26).toString()} L60 ${(y + 46).toString()} Z`}
				fill={SIDE_FILL}
				{...line}
			/>
			<path
				d={`M60 ${y.toString()} L100 ${(y + 20).toString()} L60 ${(y + 40).toString()} L20 ${(y + 20).toString()} Z`}
				fill={top}
				{...line}
			/>
			<path
				d={`M20 ${(y + 26).toString()} L60 ${(y + 6).toString()} L100 ${(y + 26).toString()} M60 ${y.toString()} V${(y + 6).toString()}`}
				fill="none"
				strokeDasharray="2 2.5"
				opacity={0.45}
				{...line}
			/>
			{children}
		</g>
	);
}

function Pins({ className, d }: { className: string; d: string }) {
	return (
		<g className={`stack-pins ${className}`} stroke="var(--info)" strokeWidth={1.2} strokeDasharray="2 2" opacity={0}>
			<path d={d} />
		</g>
	);
}

function StackArt() {
	const fine = { vectorEffect: "non-scaling-stroke" as const, strokeWidth: 1 };
	return (
		<svg viewBox="-26 0 176 122" width={200} height={139} aria-hidden="true" className="block overflow-visible">
			<defs>
				<radialGradient id="empty-stack-fade" cx="50%" cy="50%" r="50%">
					<stop offset={0.35} stopColor="#fff" />
					<stop offset={1} stopColor="#000" />
				</radialGradient>
				<mask id="empty-stack-floor" maskUnits="userSpaceOnUse" x={-16} y={-16} width={52} height={52}>
					<rect x={-14} y={-14} width={48} height={48} fill="url(#empty-stack-fade)" />
				</mask>
			</defs>
			<g transform={ISO(52)} mask="url(#empty-stack-floor)">
				<g stroke="var(--fg4)" fill="none" opacity={0.6}>
					{FLOOR.map((v) => (
						<path key={v} d={`M${v.toString()} -14 V34 M-14 ${v.toString()} H34`} {...fine} />
					))}
				</g>
				<rect
					className="stack-cell"
					width={20}
					height={20}
					fill="var(--fg)"
					fillOpacity={0.06}
					stroke="var(--fg4)"
					strokeDasharray="2 2.5"
					{...fine}
				/>
			</g>
			<Tile y={46} stroke={TILE_STROKE} top="var(--bg)" className="stack-t1">
				<g transform={ISO(46)} stroke={TILE_STROKE}>
					{HATCH.map((x, i) => (
						<path
							key={x}
							className="stack-hatch"
							style={{ animationDelay: `${(i * 0.07).toFixed(2)}s` }}
							pathLength={1}
							d={`M${x.toString()} 3.5 V16.5`}
							{...fine}
						/>
					))}
				</g>
			</Tile>
			<Pins className="stack-p2" d="M20 58 V66 M60 78 V86 M100 58 V66" />
			<Tile y={32} stroke={TILE_STROKE} top="var(--bg)" className="stack-t2">
				<g transform={ISO(32)} fill={TILE_STROKE} fillOpacity={0.24} stroke={TILE_STROKE}>
					{PODS.map(([x = 0, py = 0], i) => (
						<rect
							key={`${x.toString()}-${py.toString()}`}
							className="stack-pod"
							style={{ animationDelay: `${(i * 0.12).toFixed(2)}s` }}
							x={x}
							y={py}
							width={5.5}
							height={5.5}
							rx={0.6}
							{...fine}
						/>
					))}
				</g>
			</Tile>
			<Pins className="stack-p3" d="M20 44 V52 M60 64 V72 M100 44 V52" />
			<Tile y={18} stroke={TOP_STROKE} top="color-mix(in oklch, var(--info) 10%, var(--bg))" className="stack-t3">
				<g transform={ISO(18)} fill="none" stroke="var(--info)">
					<circle cx={10} cy={10} r={6} {...fine} strokeWidth={1.2} />
					<path d="M4 10 H16 M5.2 6.8 H14.8 M5.2 13.2 H14.8" opacity={0.75} {...fine} />
					<ellipse className="stack-meridian" cx={10} cy={10} rx={6} ry={6} {...fine} />
					<ellipse className="stack-meridian stack-meridian-b" cx={10} cy={10} rx={6} ry={6} {...fine} />
					<circle className="stack-pulse" cx={10} cy={10} r={6} {...fine} strokeWidth={1.4} />
				</g>
			</Tile>
			<path
				className="stack-trace"
				pathLength={1}
				d="M36 104 L60 92 V58 L60 38"
				fill="none"
				stroke="var(--info)"
				strokeWidth={1.6}
				strokeLinecap="round"
				strokeLinejoin="round"
			/>
		</svg>
	);
}

/** Shown wherever a page has nothing to list because no resource is deployed; `wholeWorkspace` for pages that read every environment. */
export function EmptyWorkspace({ wholeWorkspace = false }: { wholeWorkspace?: boolean }) {
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const { environments, active, setActive } = useEnvironments();
	const otherEnv = wholeWorkspace ? undefined : environments.find((e) => e.id !== active?.id);
	const scope = wholeWorkspace || active === undefined ? undefined : <InlineCode>{active.name}</InlineCode>;
	const newHref = newResourcePath(
		activeOrgId ?? "",
		activeWorkspaceId ?? "",
		active === undefined ? {} : { env: active.slug },
	);
	return (
		<EmptyHero art={<StackArt />} title="No resources" subtitle={scope}>
			<div className="my-2 flex w-[min(380px,100%)] items-center justify-between gap-6 rounded-lg bg-background py-3.5 pr-3.5 pl-4 text-left ring-1 ring-line">
				<div className="flex flex-col gap-1.5 font-mono text-[13px]">
					{COMMANDS.map((cmd) => (
						<span key={cmd}>
							<span className="text-fg4">$ </span>
							{cmd}
						</span>
					))}
				</div>
				<CopyButton text={COMMANDS.join("\n")} name="Copy commands" />
			</div>
			<div className="flex flex-wrap justify-center gap-2">
				<Button size="lg" nativeButton={false} render={<Link to={newHref} />}>
					<PlusIcon />
					New resource
				</Button>
				<Button
					variant="outline"
					size="lg"
					nativeButton={false}
					render={<a href={DOCS_GET_STARTED} target="_blank" rel="noopener noreferrer" />}
				>
					Read the docs
					<ArrowUpRightIcon />
				</Button>
			</div>
			{otherEnv !== undefined && (
				<Button
					variant="link"
					className="mt-1"
					onClick={() => {
						setActive(otherEnv);
					}}
				>
					View {otherEnv.name} instead
				</Button>
			)}
		</EmptyHero>
	);
}
