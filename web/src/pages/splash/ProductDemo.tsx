import { LoaderCircle, Minus, Plus, Rocket } from "lucide-react";

import { Button } from "@/components/design/Button";
import { cn } from "@/lib/utils";

import { ClusterDiagram } from "./ClusterDiagram";
import { MAX_REPLICAS, MIN_REPLICAS, readyCount } from "./cluster";
import { Frame } from "./primitives";
import { useRollout } from "./useRollout";

function Fact({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex min-w-0 flex-col gap-0.5 bg-background px-4 py-3">
			<span className="text-sm text-fg3">{label}</span>
			<span className="truncate font-mono text-[13px]">{value}</span>
		</div>
	);
}

export function ProductDemo() {
	const { rollout, deploy, scale } = useRollout();
	const ready = readyCount(rollout);
	const { rolling, target, tag } = rollout;

	return (
		<section id="product" className="scroll-mt-[60px]">
			<Frame className="pt-2 pb-[120px]">
				<div className="overflow-hidden rounded-[14px] border border-line bg-background shadow-[0_12px_32px_-18px_rgba(0,0,0,0.22)]">
					<div className="flex min-h-12 flex-wrap items-center gap-3 border-b border-line py-2 pr-3.5 pl-4">
						<span className="flex items-center gap-2 text-[13.5px] font-medium">
							<span className="text-fg3">team-loco</span>
							<span className="text-fg4">/</span>
							storefront
							<span className="text-fg4">/</span>
							<span className="flex items-center gap-1.5">
								<span className="size-1.5 rounded-full bg-ok-fg" />
								production
							</span>
						</span>
						<div className="flex-1" />
						<span className={cn("font-mono text-sm", rolling ? "text-warn" : "text-ok-fg")}>
							{rolling ? `rolling update · ${ready}/${target} on sha-${tag}` : "all replicas healthy"}
						</span>
						<div className="flex h-[30px] items-center overflow-hidden rounded-[7px] border border-line">
							<span className="pr-2 pl-2.5 text-[12.5px] text-fg3">api replicas</span>
							<Button
								variant="ghost"
								aria-label="Remove replica"
								disabled={rolling || target <= MIN_REPLICAS}
								onClick={() => {
									scale(target - 1);
								}}
								className="h-full w-7 rounded-none border-0 border-l border-line px-0 text-fg2"
							>
								<Minus />
							</Button>
							<span className="w-[26px] text-center font-mono text-[13px] font-semibold">{target}</span>
							<Button
								variant="ghost"
								aria-label="Add replica"
								disabled={rolling || target >= MAX_REPLICAS}
								onClick={() => {
									scale(target + 1);
								}}
								className="h-full w-7 rounded-none border-0 border-l border-line px-0 text-fg2"
							>
								<Plus />
							</Button>
						</div>
						<Button
							variant={rolling ? "secondary" : "inverted"}
							disabled={rolling}
							onClick={deploy}
							className="h-[30px] rounded-[7px] px-3 font-medium"
						>
							{rolling ? <LoaderCircle className="animate-spin" /> : <Rocket />}
							{rolling ? "Rolling out" : "Deploy api"}
						</Button>
					</div>
					<div className="overflow-x-auto">
						<div className="min-w-[900px]">
							<ClusterDiagram rollout={rollout} />
						</div>
					</div>
					<div className="grid grid-cols-2 gap-px border-t border-line bg-line md:grid-cols-4">
						<Fact label="Endpoint" value="https://api.onloco.app" />
						<Fact label="Image" value={`api:sha-${tag}`} />
						<Fact label="Strategy" value="rolling · surge 1" />
						<Fact label="Ready" value={`${ready} of ${target}`} />
					</div>
				</div>
			</Frame>
		</section>
	);
}
