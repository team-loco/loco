import type { Transport } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Navigate } from "react-router";

import { AppLoading } from "@/context/AppLoader";
import { getErrorMessage } from "@/lib/error-handler";

import { signInErrorState } from "./sign-in-error";

type Outcome = { kind: "pending" } | { kind: "done"; to: string } | { kind: "failed"; message: string };

export type AuthStepRun = (transport: Transport) => Promise<string>;

const started = new Set<string>();

export function AuthStep({ id, run, fallbackError }: { id: string; run: AuthStepRun; fallbackError: string }) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const [outcome, setOutcome] = useState<Outcome>({ kind: "pending" });

	useEffect(() => {
		if (started.has(id)) return;
		started.add(id);
		run(transport)
			.then(async (to) => {
				await queryClient.invalidateQueries();
				setOutcome({ kind: "done", to });
			})
			.catch((err: unknown) => {
				setOutcome({ kind: "failed", message: getErrorMessage(err, fallbackError) });
			});
	}, [id, run, transport, queryClient, fallbackError]);

	switch (outcome.kind) {
		case "pending":
			return <AppLoading message="Signing you in…" />;
		case "done":
			return <Navigate to={outcome.to} replace />;
		case "failed":
			return <Navigate to="/login" replace state={signInErrorState(outcome.message)} />;
	}
}
