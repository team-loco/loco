import { useMutation } from "@connectrpc/connect-query";
import { Loader2 } from "lucide-react";
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
	return PORT_PATTERN.test(port) && portNumber >= 1024 && portNumber <= 65535 && state.length >= 16 && challenge.length >= 43;
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
				{() => <p className="m-0 text-center text-fg2">Run <code>loco login</code> again to get a new link.</p>}
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
				{() => <p className="m-0 text-center text-fg2">You can return to your terminal.</p>}
			</CliCard>
		);
	}

	return (
		<CliCard title="Sign in to the Loco CLI">
			{(email) => (
				<>
					<p className="m-0 text-center text-fg2">
						A terminal on this computer is asking to sign in as{" "}
						<span className="font-semibold text-foreground">{email}</span>. Continue only if you just ran{" "}
						<code>loco login</code>.
					</p>
					{approve.error !== null && (
						<div role="alert" className="rounded-sm bg-bad-bg px-3 py-2 text-center text-sm text-bad-fg">
							{getErrorMessage(approve.error, "Could not authorize the CLI")}
						</div>
					)}
					<div className="flex flex-col gap-2">
						<Button
							variant="inverted"
							size="lg"
							className="w-full"
							disabled={approve.isPending}
							onClick={() => {
								void authorize().catch(() => undefined);
							}}
						>
							{approve.isPending && <Loader2 className="size-4 animate-spin" />}
							Authorize
						</Button>
						<Button variant="outline" size="lg" className="w-full" disabled={approve.isPending} onClick={cancel}>
							Cancel
						</Button>
					</div>
				</>
			)}
		</CliCard>
	);
}
