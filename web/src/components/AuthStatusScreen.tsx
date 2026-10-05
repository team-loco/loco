import type { ReactNode } from "react";
import { LocoLogo } from "@/components/design/LocoLogo";

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
					<LocoLogo className="mb-1 w-24" />
					<h1 className="m-0 text-xl font-semibold">{title}</h1>
					{description !== undefined && <p className="m-0 text-fg3">{description}</p>}
				</div>
				{children}
			</div>
		</div>
	);
}
