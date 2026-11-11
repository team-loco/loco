import { Spinner } from "@/components/design/Spinner";

import { useSignIn } from "@/auth/SignIn";
import { Button } from "@/components/design/Button";

import { AuthCard } from "./AuthCard";

export function SignInCard({
	subtitle,
	next,
	loginHint,
	error,
}: {
	subtitle?: string | undefined;
	next: string | null;
	loginHint: string | null;
	error: string | null;
}) {
	const { signIn, redirecting } = useSignIn();

	return (
		<AuthCard title="Sign in to loco" subtitle={subtitle} error={error}>
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
