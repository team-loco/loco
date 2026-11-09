import { CheckIcon, CopyIcon, ExternalLinkIcon, PlusIcon } from "lucide-react";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";
import { EmptyHero } from "@/components/design/EmptyHero";
import { useCopy } from "@/hooks/useCopy";
import { DOCS_URL } from "@/pages/splash/links";

const COMMANDS = ["loco login", "loco deploy"];
const COPY_KEY = "cli";
const TILE_STROKE = "color-mix(in oklch, var(--fg) 42%, var(--bg))";
const TOP_STROKE = "color-mix(in oklch, var(--info) 70%, var(--bg))";
const SIDE_FILL = "color-mix(in oklch, var(--panel2) 70%, var(--line2))";
const ISO = (y: number) => `matrix(2 1 -2 1 60 ${y.toString()})`;
const HATCH = [4, 6.5, 9, 11.5, 14, 16.5];
const PODS = [
	[4, 4],
	[10.5, 4],
	[4, 10.5],
	[10.5, 10.5],
];

function Tile({ y, stroke, top }: { y: number; stroke: string; top: string }) {
	const line = { stroke, strokeWidth: 1, strokeLinejoin: "round" as const };
	return (
		<>
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
		</>
	);
}

function StackArt() {
	const fine = { vectorEffect: "non-scaling-stroke" as const, strokeWidth: 1 };
	return (
		<svg viewBox="0 0 120 100" width={172} height={143} aria-hidden="true" className="block">
			<Tile y={46} stroke={TILE_STROKE} top="var(--bg)" />
			<g transform={ISO(46)} stroke={TILE_STROKE} opacity={0.6}>
				{HATCH.map((x) => (
					<path key={x} d={`M${x.toString()} 3.5 V16.5`} {...fine} />
				))}
			</g>
			<Tile y={32} stroke={TILE_STROKE} top="var(--bg)" />
			<g transform={ISO(32)} fill={TILE_STROKE} fillOpacity={0.24} stroke={TILE_STROKE}>
				{PODS.map(([x = 0, py = 0]) => (
					<rect key={`${x.toString()}-${py.toString()}`} x={x} y={py} width={5.5} height={5.5} rx={0.6} {...fine} />
				))}
			</g>
			<Tile y={18} stroke={TOP_STROKE} top="color-mix(in oklch, var(--info) 10%, var(--bg))" />
			<g transform={ISO(18)} fill="none" stroke="var(--info)">
				<circle cx={10} cy={10} r={6} {...fine} />
				<path d="M4 10 H16 M5.2 6.8 H14.8 M5.2 13.2 H14.8" opacity={0.75} {...fine} />
			</g>
		</svg>
	);
}

export function EmptyWorkspace({
	envName,
	newHref,
	otherEnvName,
	onSwitchEnv,
}: {
	envName: string;
	newHref: string;
	otherEnvName: string | null;
	onSwitchEnv: () => void;
}) {
	const [copied, copy] = useCopy();
	const done = copied === COPY_KEY;
	return (
		<EmptyHero
			art={<StackArt />}
			title="No resources yet"
			subtitle={`Nothing is deployed to ${envName}. Deploy from your terminal with the loco CLI, or create a resource here.`}
		>
			<div className="my-2 flex w-[min(380px,100%)] items-center justify-between gap-6 rounded-lg bg-background py-3.5 pr-3.5 pl-4 text-left ring-1 ring-line">
				<div className="flex flex-col gap-1.5 font-mono text-[13px]">
					{COMMANDS.map((cmd) => (
						<span key={cmd}>
							<span className="text-fg4">$ </span>
							{cmd}
						</span>
					))}
				</div>
				<Button
					variant="outline"
					size="sm"
					aria-label={done ? "Copied" : "Copy commands"}
					className={done ? "text-ok-fg" : undefined}
					onClick={() => {
						copy(COPY_KEY, COMMANDS.join("\n"));
					}}
				>
					{done ? <CheckIcon /> : <CopyIcon />}
					{done ? "Copied" : "Copy"}
				</Button>
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
					render={<a href={DOCS_URL} target="_blank" rel="noopener noreferrer" />}
				>
					Read the docs
					<ExternalLinkIcon />
				</Button>
			</div>
			{otherEnvName !== null && (
				<Button variant="link" className="mt-1" onClick={onSwitchEnv}>
					View {otherEnvName} instead
				</Button>
			)}
		</EmptyHero>
	);
}
