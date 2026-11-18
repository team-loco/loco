import type { ReactNode } from "react";
import { useLocation } from "react-router";

import { useAuth } from "@/auth/AuthProvider";
import { AppLoading } from "@/context/AppLoader";
import { getErrorMessage } from "@/lib/error-handler";
import { AuthCard } from "@/pages/auth/AuthCard";
import { SignInCard } from "@/pages/auth/SignInCard";

export function CliCard({
	title,
	error,
	children,
}: {
	title: string;
	error?: string | null | undefined;
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
			/>
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
