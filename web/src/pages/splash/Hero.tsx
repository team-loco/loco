import { Check, Copy } from "lucide-react";
import { Button } from "@/components/design/Button";
import { useCopy } from "@/hooks/useCopy";
import { cn } from "@/lib/utils";
import { INSTALL_COMMAND } from "./links";
import { Frame } from "./primitives";

export function Hero() {
  const [copied, copy] = useCopy(1500);
  return (
    <section className="relative overflow-hidden">
      <Frame className="grid items-center gap-12 py-20 md:grid-cols-[minmax(0,1fr)_minmax(0,0.9fr)] md:gap-16 md:py-32">
        <div className="relative z-10 flex flex-col items-start">
          <p className="mb-6 font-mono text-[12px] uppercase tracking-[0.13em] text-fg3">Open-source application deployment</p>
          <h1 className="m-0 max-w-[780px] text-[clamp(48px,6.1vw,88px)] font-semibold leading-[1.01] tracking-[-0.055em] text-balance">
            Your code.<br/><span className="text-primary">Our infrastructure.</span>
          </h1>
          <p className="mt-7 max-w-[520px] text-[18px] leading-[1.6] text-fg2">
            Bring a Dockerfile or container image. Loco handles building, deploying, HTTPS, scaling, and the tools to understand what runs.
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-4">
            <Button size="xl" nativeButton={false} render={<a href="#start" />} className="h-11 bg-foreground px-5 text-background hover:bg-fg2">Get started</Button>
            <a href="https://github.com/team-loco/loco" target="_blank" rel="noopener noreferrer" className="text-[14px] font-medium text-fg2 hover:text-foreground">Explore the source ↗</a>
          </div>
          <button onClick={() => copy("install", INSTALL_COMMAND)} className="mt-8 flex max-w-full items-center gap-4 rounded-md border border-line bg-bg2 px-4 py-3 text-left font-mono text-[12px] text-fg2 transition-colors hover:border-line2" aria-label="Copy installation command">
            <span className="text-primary">$</span><span className="break-all">{INSTALL_COMMAND}</span>
            <span className={cn("shrink-0", copied === "install" ? "text-ok-fg" : "text-fg3")}>{copied === "install" ? <Check size={14}/> : <Copy size={14}/>}</span>
          </button>
        </div>
        <div className="relative min-h-[365px] overflow-hidden rounded-lg border border-line bg-bg2 p-6 md:min-h-[465px] md:p-8">
          <div className="pointer-events-none absolute inset-0 opacity-60" style={{backgroundImage:"linear-gradient(var(--line) 1px, transparent 1px), linear-gradient(90deg, var(--line) 1px, transparent 1px)",backgroundSize:"36px 36px"}}/>
          <div className="relative flex h-full flex-col justify-between gap-5">
            <div className="flex items-center justify-between font-mono text-[11px] uppercase tracking-wide text-fg3"><span>Deployment / production</span><span>01 — 03</span></div>
            <div className="rounded-md border border-line bg-background p-5 shadow-sm">
              <div className="mb-3 text-[12px] font-medium text-fg3">loco.yaml</div>
              <pre className="overflow-x-auto font-mono text-[13px] leading-[1.9] text-foreground">{`services:
  api:
    dockerfile: Dockerfile
    context: .
    regions:
      us-east-1:
        replicas: 2`}</pre>
            </div>
            <div className="flex items-center gap-3 font-mono text-[12px]"><span className="text-fg3">$</span><span>loco deploy</span><span className="ml-auto text-fg3">build → rollout</span></div>
            <div className="rounded-md border border-line bg-background p-5">
              <div className="flex items-center justify-between gap-4"><strong className="text-[15px]">api</strong><span className="flex items-center gap-2 text-[12px] text-ok-fg"><span className="size-1.5 rounded-full bg-ok-fg"/>2 / 2 ready</span></div>
              <div className="mt-3 border-t border-line pt-3 font-mono text-[12px] text-fg2">https://api.onloco.app</div>
            </div>
          </div>
        </div>
      </Frame>
    </section>
  );
}
