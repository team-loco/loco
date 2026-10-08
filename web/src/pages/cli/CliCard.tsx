import type { ReactNode } from "react";
import { useLocation } from "react-router";

import { useAuth } from "@/auth/AuthProvider";
import { SignInRedirect } from "@/auth/SignIn";
import { LocoLogo } from "@/components/design/LocoLogo";
import { AppLoading } from "@/context/AppLoader";

export function CliCard({ title, children }: { title: string; children: (email: string) => ReactNode }) {
	const { user, signedOut, isPending } = useAuth();
	const location = useLocation();

	if (signedOut) {
		return <SignInRedirect next={location.pathname + location.search} />;
	}
	if (isPending || user === null) return <AppLoading />;

	return (
		<div className="flex min-h-screen items-start justify-center bg-background px-4 pt-[14vh] text-foreground">
			<div className="flex w-full max-w-[400px] flex-col gap-5 rounded-xl border border-line bg-background px-8 pt-9 pb-7 shadow-popover">
				<div className="flex flex-col items-center gap-3 text-center">
					<LocoLogo motion="once" drawKey="cli" className="mb-1 w-24" />
					<h1 className="m-0 text-xl font-semibold text-balance">{title}</h1>
				</div>
				{children(user.email)}
			</div>
		</div>
	);
}
