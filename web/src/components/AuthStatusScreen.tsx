import { Loader2 } from "lucide-react";
import type { ReactNode } from "react";

export function AuthStatusScreen({
	title,
	description,
	children,
}: {
	title: ReactNode;
	description?: ReactNode;
	children?: ReactNode;
}) {
	return (
		<div className="flex min-h-screen items-center justify-center bg-bg2 px-4">
			<div className="flex w-full max-w-[400px] flex-col gap-5 rounded-lg border border-line bg-background p-8">
				<div className="flex flex-col items-center gap-3 text-center">
					<img src="/logo.webp" alt="Loco" className="size-10 rounded-lg" />
					<h1 className="m-0 text-xl font-semibold">{title}</h1>
					{description !== undefined && <p className="m-0 text-fg3">{description}</p>}
				</div>
				{children ?? (
					<div role="status" aria-live="polite" className="flex justify-center">
						<Loader2 className="size-5 animate-spin text-fg3" />
					</div>
				)}
			</div>
		</div>
	);
}
