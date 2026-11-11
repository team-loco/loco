import { Spinner } from "@/components/design/Spinner";
import { useMutation } from "@connectrpc/connect-query";
import { useState } from "react";
import { useSearchParams } from "react-router";

import { approveCLILogin } from "@gen/loco/auth/v1/auth-AuthService_connectquery";

import { forgetNextPath } from "@/auth/next";
import { Button } from "@/components/design/Button";
import { getErrorMessage } from "@/lib/error-handler";

import { CliCard } from "./CliCard";

const PORT_PATTERN = /^\d{4,5}$/;

function loopbackURL(port: string, params: Record<string, string>): string {
	const url = new URL(`http://127.0.0.1:${port}/callback`);
	for (const [key, value] of Object.entries(params)) url.searchParams.set(key, value);
	return url.toString();
}

function validRequest(port: string | null, state: string | null, challenge: string | null): boolean {
	if (port === null || state === null || challenge === null) return false;
	const portNumber = Number(port);
	return (
		PORT_PATTERN.test(port) && portNumber >= 1024 && portNumber <= 65535 && state.length >= 16 && challenge.length >= 43
	);
}

export function CliLogin() {
	const [params] = useSearchParams();
	const port = params.get("port");
	const state = params.get("state");
	const challenge = params.get("challenge");
	const [done, setDone] = useState<"approved" | "canceled" | null>(null);
	const approve = useMutation(approveCLILogin);

	if (!validRequest(port, state, challenge) || port === null || state === null || challenge === null) {
		return (
			<CliCard title="This sign-in link is incomplete">
				{() => (
					<p className="m-0 text-center text-[13.5px] text-fg2">
						Run <code className="font-mono text-[0.92em] whitespace-nowrap text-foreground">loco login</code> again to
						get a new link.
					</p>
				)}
			</CliCard>
		);
	}

	const authorize = async () => {
		const { code } = await approve.mutateAsync({ state, codeChallenge: challenge });
		forgetNextPath();
		setDone("approved");
		window.location.assign(loopbackURL(port, { code, state }));
	};

	const cancel = () => {
		forgetNextPath();
		setDone("canceled");
		window.location.assign(loopbackURL(port, { error: "access_denied", state }));
	};

	if (done !== null) {
		return (
			<CliCard title={done === "approved" ? "You're signed in" : "Sign-in canceled"}>
				{() => <p className="m-0 text-center text-[13.5px] text-fg2">You can return to your terminal.</p>}
			</CliCard>
		);
	}

	return (
		<CliCard
			title="Sign in to the loco CLI"
			error={approve.error !== null ? getErrorMessage(approve.error, "Could not authorize the CLI") : null}
		>
			{(email) => (
				<>
					<p className="m-0 text-center text-[13.5px] text-fg2">
						A terminal on this computer is asking to sign in as{" "}
						<span className="font-medium text-foreground">{email}</span>. Continue only if you just ran{" "}
						<code className="font-mono text-[0.92em] whitespace-nowrap text-foreground">loco login</code>.
					</p>
					<div className="flex flex-col gap-2">
						<Button
							size="xl"
							className="w-full"
							disabled={approve.isPending}
							onClick={() => {
								void authorize().catch(() => undefined);
							}}
						>
							{approve.isPending && <Spinner className="size-4" />}
							Authorize
						</Button>
						<Button variant="outline" size="xl" className="w-full" disabled={approve.isPending} onClick={cancel}>
							Cancel
						</Button>
					</div>
				</>
			)}
		</CliCard>
	);
}
