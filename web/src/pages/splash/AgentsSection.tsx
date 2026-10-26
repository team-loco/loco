import { Check, Copy } from "lucide-react";
import { useCopy } from "@/hooks/useCopy";
import { Frame } from "./primitives";

const PROMPT = "Help me deploy this repository with Loco. Read https://github.com/team-loco/loco/blob/main/README.md and inspect the repo before suggesting changes. Use the documented loco.yaml and CLI commands. Ask before deploying or making billable changes.";

export function AgentsSection() {
  const [copied, copy] = useCopy(1500);
  return (
    <section id="agents" className="scroll-mt-16 border-t border-line bg-bg2">
      <Frame className="grid gap-12 py-24 md:grid-cols-[0.9fr_1.1fr] md:items-center">
        <div>
          <p className="mb-4 font-mono text-[12px] uppercase tracking-[0.12em] text-fg3">For developers and coding agents</p>
          <h2 className="m-0 text-[clamp(32px,4.5vw,54px)] font-semibold leading-[1.08] tracking-[-0.04em]">Your agent can help you ship.</h2>
          <p className="mt-5 max-w-[440px] text-[16px] leading-[1.65] text-fg2">Loco has a command-line workflow your coding agent can follow. Start with the public documentation and review any changes before deployment.</p>
          <p className="mt-5 text-[13px] text-fg3">A dedicated Loco agent skill is planned; this prompt works with today's published docs.</p>
        </div>
        <div className="overflow-hidden rounded-lg border border-line bg-background">
          <div className="flex items-center justify-between border-b border-line px-5 py-3"><span className="font-mono text-[12px] text-fg3">Agent prompt</span><button onClick={() => copy("prompt", PROMPT)} className="flex items-center gap-2 text-[12px] text-fg2 hover:text-foreground">{copied==="prompt" ? <Check size={14}/> : <Copy size={14}/>} {copied==="prompt"?"Copied":"Copy prompt"}</button></div>
          <p className="m-0 whitespace-pre-wrap px-5 py-7 font-mono text-[13px] leading-[1.75]">{PROMPT}</p>
          <div className="border-t border-line bg-bg2 px-5 py-3 text-[12px] text-fg3">Works as a starting instruction for your coding agent.</div>
        </div>
      </Frame>
    </section>
  );
}
