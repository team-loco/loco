import { Button } from "@/components/design/Button";
import { LocoLogo } from "@/components/design/LocoLogo";
import { DOCS_URL, GITHUB_URL } from "./links";

export function SplashNav({ onSignIn, redirecting }: {onSignIn: () => void; redirecting: boolean}) {
  return (
    <header className="sticky top-0 z-30 border-b border-line bg-background/95 backdrop-blur-md">
      <div className="mx-auto flex h-[64px] max-w-[1200px] items-center gap-7 px-4 sm:px-7">
        <a href="/" aria-label="Loco home"><LocoLogo className="w-[86px]"/></a>
        <nav className="hidden items-center gap-7 text-[13px] text-fg2 md:flex">
          <a href="#product" className="hover:text-foreground">Product</a>
          <a href="#start" className="hover:text-foreground">Get started</a>
          <a href="#agents" className="hover:text-foreground">For agents</a>
          <a href={DOCS_URL} target="_blank" rel="noopener noreferrer" className="hover:text-foreground">API docs ↗</a>
          <a href={GITHUB_URL} target="_blank" rel="noopener noreferrer" className="hover:text-foreground">GitHub ↗</a>
        </nav>
        <div className="flex-1"/>
        <Button variant="ghost" onClick={onSignIn} disabled={redirecting} className="text-[13px]">Sign in</Button>
        <Button nativeButton={false} render={<a href="#start"/>} className="hidden h-9 bg-foreground px-4 text-background hover:bg-fg2 sm:inline-flex">Get started</Button>
      </div>
    </header>
  );
}
