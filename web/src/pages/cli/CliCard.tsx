import type { ReactNode } from "react";
import { useLocation } from "react-router";

import { useAuth } from "@/auth/AuthProvider";
import { AppLoading } from "@/context/AppLoader";
import { getErrorMessage } from "@/lib/error-handler";
import { AuthCard } from "@/pages/auth/AuthCard";
import { SignInCard } from "@/pages/auth/SignInCard";

import { TerminalCode } from "./TerminalCode";

export function CliCard({
	title,
	error,
	terminalCode = null,
	children,
}: {
	title: string;
	error?: string | null | undefined;
	terminalCode?: string | null;
	children: (email: string) => ReactNode;
}) {
	const { user, signedOut, isPending, error: authError } = useAuth();
	const location = useLocation();

	if (signedOut) {
		return (
			<SignInCard
				subtitle="Finish signing in to the loco CLI."
				next={location.pathname + location.search + location.hash}
				loginHint={null}
				error={null}
				autoStart={terminalCode === null}
			>
				{terminalCode !== null && <TerminalCode code={terminalCode} />}
			</SignInCard>
		);
	}
	if (isPending) return <AppLoading />;
	if (user === null) {
		return (
			<SignInCard
				next={location.pathname + location.search + location.hash}
				loginHint={null}
				error={getErrorMessage(authError, "Could not load your account")}
			/>
		);
	}

	return (
		<AuthCard title={title} error={error}>
			{children(user.email)}
		</AuthCard>
	);
}
