import { Button } from "@/components/design/Button";
import { Frame } from "./primitives";

export function AccessSection({ onSignIn, redirecting }: {onSignIn: () => void; redirecting: boolean}) {
  return (
    <section id="access" className="scroll-mt-20 border-t border-line bg-foreground text-background">
      <Frame className="grid gap-12 py-20 md:grid-cols-[1.1fr_0.9fr] md:items-end md:py-28">
        <div>
          <p className="mb-5 font-mono text-[12px] uppercase tracking-wider text-background/55">Ready to deploy?</p>
          <h2 className="m-0 max-w-[600px] text-[clamp(38px,5vw,64px)] font-semibold leading-[1.07] tracking-[-0.045em]">Your code.<br/>Our infrastructure.</h2>
          <p className="mt-5 max-w-[480px] text-[16px] text-background/65">Start with the CLI, or explore the open-source project.</p>
        </div>
        <div className="flex flex-wrap gap-3 md:justify-end">
          <Button onClick={onSignIn} disabled={redirecting} className="h-11 bg-background px-6 text-foreground hover:bg-background/85">Sign in</Button>
          <a href="https://github.com/team-loco/loco" target="_blank" rel="noopener noreferrer" className="inline-flex h-11 items-center rounded-md border border-background/30 px-5 text-[14px] hover:bg-background/10">View on GitHub ↗</a>
        </div>
      </Frame>
    </section>
  );
}
