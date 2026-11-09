import { DeployDemo } from "./DeployDemo";
import { SplashCtas } from "./SplashCtas";

export function Hero({ onSignIn, redirecting }: { onSignIn: () => void; redirecting: boolean }) {
	return (
		<section className="relative pt-14 sm:pt-24">
			<div
				aria-hidden="true"
				className="absolute inset-x-0 top-0 bottom-[30%] -z-10 bg-[radial-gradient(color-mix(in_oklch,var(--fg)_9%,transparent)_1px,transparent_1px)] bg-size-[22px_22px] bg-top mask-[linear-gradient(#000_50%,transparent)]"
			/>
			<div className="mx-auto flex w-full max-w-[1160px] flex-col items-center gap-[22px] px-4 pb-16 text-center sm:px-7">
				<h1 className="m-0 text-[clamp(44px,6.4vw,78px)] leading-[1.02] font-medium tracking-[-0.045em] text-balance">
					From Dockerfile
					<br />
					to production.
				</h1>
				<p className="m-0 max-w-[540px] text-[18px] text-pretty text-fg3">
					Run <span className="font-mono text-[16px] text-foreground">loco deploy</span> and your container is live with
					HTTPS, rolling deploys, logs and metrics.
				</p>
				<SplashCtas onSignIn={onSignIn} redirecting={redirecting} />
			</div>
			<div className="mx-auto w-full max-w-[1160px] px-4 sm:px-7">
				<DeployDemo />
			</div>
		</section>
	);
}
