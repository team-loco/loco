import { Code, ConnectError, createClient, type Transport } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Navigate } from "react-router";

import { OAuthProvider, OAuthService } from "@gen/loco/oauth/v1/oauth_pb";
import { OrgService } from "@gen/loco/org/v1/org_pb";
import { UserService } from "@gen/loco/user/v1/user_pb";
import { WorkspaceService } from "@gen/loco/workspace/v1/workspace_pb";

import { AppLoading } from "@/context/AppLoader";
import { getErrorMessage } from "@/lib/error-handler";
import { workspacePath } from "@/lib/routes";

type Outcome = { kind: "pending"; message: string } | { kind: "done"; to: string } | { kind: "failed"; error: string };

let started = false;

function alreadyExists(err: unknown): boolean {
	return err instanceof ConnectError && err.code === Code.AlreadyExists;
}

async function ensureDefaultOrg(transport: Transport, userId: string, email: string): Promise<string> {
	const orgs = createClient(OrgService, transport);
	try {
		const created = await orgs.createOrg({ name: email });
		return created.orgId;
	} catch (err) {
		if (!alreadyExists(err)) throw err;
		const { orgs: existing } = await orgs.listUserOrgs({ userId });
		const first = existing[0];
		if (first === undefined) throw new Error("Your organization exists but could not be loaded");
		return first.id;
	}
}

async function ensureDefaultWorkspace(transport: Transport, orgId: string): Promise<string> {
	const workspaces = createClient(WorkspaceService, transport);
	try {
		const created = await workspaces.createWorkspace({ orgId, name: "default" });
		return created.workspaceId;
	} catch (err) {
		if (!alreadyExists(err)) throw err;
		const { workspaces: existing } = await workspaces.listOrgWorkspaces({ orgId });
		const first = existing[0];
		if (first === undefined) throw new Error("Your workspace exists but could not be loaded");
		return first.id;
	}
}

async function signIn(transport: Transport, report: (message: string) => void): Promise<string> {
	const params = new URLSearchParams(window.location.search);
	const providerError = params.get("error");
	if (providerError !== null) throw new Error(params.get("error_description") ?? "GitHub sign-in failed");
	const code = params.get("code");
	const state = params.get("state");
	if (code === null || state === null) throw new Error("The sign-in link is missing its code");

	const { userId } = await createClient(OAuthService, transport).exchangeOAuthCode({
		code,
		state,
		redirectUri: `${window.location.origin}/oauth/callback`,
		provider: OAuthProvider.GITHUB,
	});

	const { orgs } = await createClient(OrgService, transport).listUserOrgs({ userId });
	if (orgs.length > 0) return "/dashboard";

	const { user } = await createClient(UserService, transport).whoAmI({});
	report("Creating your organization…");
	const orgId = await ensureDefaultOrg(transport, userId, user?.email ?? userId);
	report("Creating your workspace…");
	const workspaceId = await ensureDefaultWorkspace(transport, orgId);
	return workspacePath(orgId, workspaceId);
}

export function OAuthCallback() {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const [outcome, setOutcome] = useState<Outcome>({ kind: "pending", message: "Signing you in…" });

	useEffect(() => {
		if (started) return;
		started = true;
		signIn(transport, (message) => {
			setOutcome({ kind: "pending", message });
		})
			.then(async (to) => {
				await queryClient.invalidateQueries();
				setOutcome({ kind: "done", to });
			})
			.catch((err: unknown) => {
				setOutcome({ kind: "failed", error: getErrorMessage(err, "Sign-in failed") });
			});
	}, [transport, queryClient]);

	switch (outcome.kind) {
		case "pending":
			return <AppLoading message={outcome.message} />;
		case "done":
			return <Navigate to={outcome.to} replace />;
		case "failed":
			return <Navigate to="/login" replace state={{ oauthError: outcome.error }} />;
	}
}
