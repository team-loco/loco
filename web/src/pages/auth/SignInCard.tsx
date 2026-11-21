import { useEffect, useRef, type ReactNode } from "react";

import { Spinner } from "@/components/design/Spinner";

import { useSignIn } from "@/auth/SignIn";
import { Button } from "@/components/design/Button";
import { AppLoading } from "@/context/AppLoader";

import { AuthCard } from "./AuthCard";

export function SignInCard({
	subtitle,
	next,
	loginHint,
	error,
	autoStart = true,
	children,
}: {
	subtitle?: string | undefined;
	next: string | null;
	loginHint: string | null;
	error: string | null;
	autoStart?: boolean;
	children?: ReactNode;
}) {
	const { signIn, redirecting, failed } = useSignIn();
	const automatic = autoStart && error === null && !failed;
	const startedRef = useRef(false);

	useEffect(() => {
		if (!automatic || startedRef.current) return;
		startedRef.current = true;
		void signIn(next, loginHint);
	}, [automatic, signIn, next, loginHint]);

	if (automatic) return <AppLoading />;

	return (
		<AuthCard title="Sign in to loco" subtitle={subtitle} error={redirecting ? null : error}>
			{children}
			<Button
				size="xl"
				className="w-full"
				disabled={redirecting}
				onClick={() => {
					void signIn(next, loginHint);
				}}
			>
				{redirecting && <Spinner className="size-4" />}
				<span className="min-w-0 truncate">{loginHint === null ? "Continue" : `Continue as ${loginHint}`}</span>
			</Button>
		</AuthCard>
	);
}
