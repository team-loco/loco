import { useState } from "react";

import { cn } from "@/lib/utils";

import { GITHUB_URL } from "./links";
import { Section, SectionTitle } from "./primitives";

const STACK = [
	{ layer: "Edge", parts: ["Envoy Gateway", "HTTP/3", "TLS"] },
	{ layer: "Certificates", parts: ["cert-manager", "Let's Encrypt"] },
	{ layer: "Network", parts: ["NetworkPolicy CNI"] },
	{ layer: "Compute", parts: ["Kubernetes"] },
	{ layer: "Telemetry", parts: ["OpenTelemetry", "ClickHouse"] },
	{ layer: "Control plane", parts: ["Go", "Connect RPC", "PostgreSQL"] },
] as const;

export function Architecture() {
	const [highlighted, setHighlighted] = useState(0);

	return (
		<Section
			id="stack"
			className="grid items-start gap-8 min-[900px]:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)] min-[900px]:gap-12"
		>
			<div>
				<SectionTitle>Open source, end to end.</SectionTitle>
				<p className="-mt-6 mb-5 max-w-[420px] text-[15.5px] text-pretty text-fg3">
					Every layer is open source, from the edge to the control plane.
				</p>
				<a
					href={GITHUB_URL}
					target="_blank"
					rel="noopener"
					className="border-b border-line2 pb-px text-[13px] whitespace-nowrap text-fg2 transition-colors hover:text-foreground"
				>
					Read the source
				</a>
			</div>
			<div className="flex flex-col overflow-hidden rounded-overlay border border-line">
				{STACK.map(({ layer, parts }, i) => {
					const on = highlighted === i;
					return (
						<div
							key={layer}
							onMouseEnter={() => {
								setHighlighted(i);
							}}
							className={cn(
								"grid items-center gap-2 px-[18px] py-3.5 transition-colors duration-150 not-first:border-t not-first:border-line min-[520px]:grid-cols-[130px_1fr] min-[520px]:gap-4",
								on && "bg-panel",
							)}
						>
							<span className={cn("text-[13px] transition-colors", on ? "text-foreground" : "text-fg3")}>{layer}</span>
							<span className="flex flex-wrap gap-1.5">
								{parts.map((part) => (
									<span
										key={part}
										className={cn(
											"rounded-[5px] border bg-background px-2 py-[3px] font-mono text-xs text-fg2 transition-colors",
											on ? "border-[color-mix(in_oklch,var(--brand)_45%,var(--line2))]" : "border-line2",
										)}
									>
										{part}
									</span>
								))}
							</span>
						</div>
					);
				})}
			</div>
		</Section>
	);
}
