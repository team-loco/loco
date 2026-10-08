import type { Transport } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Navigate } from "react-router";

import { AppLoading } from "@/context/AppLoader";
import { toastConnectError } from "@/lib/error-handler";

type Outcome = { kind: "pending"; message: string } | { kind: "done"; to: string } | { kind: "failed" };

export type AuthStepRun = (transport: Transport, report: (message: string) => void) => Promise<string>;

const started = new Set<string>();

export function AuthStep({ id, run, fallbackError }: { id: string; run: AuthStepRun; fallbackError: string }) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const [outcome, setOutcome] = useState<Outcome>({ kind: "pending", message: "Signing you in…" });

	useEffect(() => {
		if (started.has(id)) return;
		started.add(id);
		run(transport, (message) => {
			setOutcome({ kind: "pending", message });
		})
			.then(async (to) => {
				await queryClient.invalidateQueries();
				setOutcome({ kind: "done", to });
			})
			.catch((err: unknown) => {
				toastConnectError(err, fallbackError);
				setOutcome({ kind: "failed" });
			});
	}, [id, run, transport, queryClient, fallbackError]);

	switch (outcome.kind) {
		case "pending":
			return <AppLoading message={outcome.message} />;
		case "done":
			return <Navigate to={outcome.to} replace />;
		case "failed":
			return <Navigate to="/login" replace />;
	}
}
