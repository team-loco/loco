import { ArrowUpRightIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { ART_SOFT, ART_STRONG, ClusterArt } from "@/components/design/ClusterArt";
import { CopyButton } from "@/components/design/CopyButton";
import { EmptyHero } from "@/components/design/EmptyHero";
import { DOCS_URL } from "@/pages/splash/links";

const DEPLOY_COMMAND = "loco deploy";
const ROUTES = ["M177 38 C177 52 156 50 156 64", "M183 38 C183 52 183 50 183 64", "M189 38 C189 52 210 50 210 64"];
const SERVICES = [143, 170, 197];

function ResourcesArt() {
	return (
		<ClusterArt>
			<rect
				x={163}
				y={27}
				width={40}
				height={11}
				rx={3}
				fill="var(--panel)"
				stroke={ART_STRONG}
				strokeDasharray="2 1.5"
			/>
			<rect x={169} y={31} width={28} height={3} rx={1.5} fill={ART_SOFT} />
			{ROUTES.map((d) => (
				<path key={d} d={d} fill="none" stroke={ART_SOFT} />
			))}
			{SERVICES.map((x) => (
				<g key={x}>
					<rect x={x} y={64} width={26} height={16} rx={3} fill="var(--panel)" stroke={ART_STRONG} />
					<rect x={x + 7} y={70.5} width={13} height={3} rx={1.5} fill={ART_SOFT} />
					<circle cx={x + 3.5} cy={72} r={1.8} fill="var(--ok)" />
				</g>
			))}
		</ClusterArt>
	);
}

export function ResourcesEmpty() {
	return (
		<EmptyHero
			art={<ResourcesArt />}
			title="Deploy your first resource"
			subtitle="Run loco deploy in a folder with a Dockerfile, or start one from New resource."
		>
			<div className="my-2 flex w-[min(320px,100%)] items-center justify-between gap-6 rounded-lg bg-background py-3 pr-3 pl-4 text-left ring-1 ring-line">
				<span className="font-mono text-[13px]">
					<span className="text-fg4">$ </span>
					{DEPLOY_COMMAND}
				</span>
				<CopyButton text={DEPLOY_COMMAND} name="Copy loco deploy" />
			</div>
			<Button
				variant="outline"
				nativeButton={false}
				render={<a href={DOCS_URL} target="_blank" rel="noopener noreferrer" />}
			>
				Read the docs
				<ArrowUpRightIcon />
			</Button>
		</EmptyHero>
	);
}
