import { useState } from "react";

import { cn } from "@/lib/utils";

import { CornerMarks, Frame, SectionHeading } from "./primitives";

const STACK = [
	{ layer: "Edge", parts: ["Envoy Gateway", "HTTP/3", "TLS termination"] },
	{ layer: "Certificates", parts: ["cert-manager", "Let's Encrypt"] },
	{ layer: "Network", parts: ["Cilium CNI"] },
	{ layer: "Compute", parts: ["Kubernetes"] },
	{ layer: "Telemetry", parts: ["OpenTelemetry", "ClickHouse", "Grafana"] },
	{ layer: "Control plane", parts: ["Go", "Connect RPC", "PostgreSQL"] },
] as const;

export function Architecture() {
	const [highlighted, setHighlighted] = useState(0);

	return (
		<section id="stack" className="scroll-mt-[60px] border-t border-line">
			<Frame className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,380px),1fr))] items-start gap-14 py-28">
				<CornerMarks />
				<SectionHeading index="03" eyebrow="Architecture" title="Open source, end to end." className="max-w-[420px]">
					<p className="m-0 text-xl leading-[1.55] text-pretty text-fg2">
						Every request passes through these layers, from the edge to the control plane. No proprietary runtime.
					</p>
				</SectionHeading>
				<div className="relative flex flex-col overflow-hidden rounded-[12px] border border-line bg-background">
					{STACK.map(({ layer, parts }, i) => {
						const on = highlighted === i;
						return (
							<div
								key={layer}
								onMouseEnter={() => {
									setHighlighted(i);
								}}
								className={cn(
									"grid grid-cols-[120px_minmax(0,1fr)] items-center gap-6 px-5 py-4 transition-colors duration-150 not-last:border-b not-last:border-line",
									on && "bg-primary/[0.05]"
								)}
							>
								<span
									className={cn(
										"font-mono text-sm tracking-[0.04em] uppercase transition-colors",
										on ? "text-primary" : "text-fg3"
									)}
								>
									{layer}
								</span>
								<span className="flex flex-wrap gap-1.5">
									{parts.map((part) => (
										<span
											key={part}
											className={cn(
												"inline-flex h-[26px] items-center rounded-[6px] border bg-background px-2.5 text-[13px] font-medium transition-colors",
												on ? "border-primary/30" : "border-line"
											)}
										>
											{part}
										</span>
									))}
								</span>
							</div>
						);
					})}
					<div aria-hidden="true" className="pointer-events-none absolute top-0 bottom-0 left-[152px] w-px bg-line" />
					<div
						aria-hidden="true"
						className="pointer-events-none absolute left-[150px] h-[60px] w-[5px] animate-stack-fall rounded-[3px] bg-primary motion-reduce:hidden"
					/>
				</div>
			</Frame>
		</section>
	);
}
