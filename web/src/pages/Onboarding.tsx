import { useQuery } from "@connectrpc/connect-query";
import { useEffect } from "react";
import { useNavigate } from "react-router";

import { whoAmI } from "@gen/loco/user/v1/user-UserService_connectquery";

import { AuthStatusScreen } from "@/components/AuthStatusScreen";
import { useAppLoader } from "@/context/AppLoader";
import { Button } from "@/components/design/Button";
import { useAutoCreateOrgWorkspace } from "@/hooks/useAutoCreateOrgWorkspace";
import { workspacePath } from "@/lib/routes";

type OnboardingStep = ReturnType<typeof useAutoCreateOrgWorkspace>["step"];

let autoCreateStarted = false;

function stepLabel(step: OnboardingStep): string {
	switch (step) {
		case "creating-org":
			return "Creating your organization...";
		case "creating-workspace":
			return "Creating your workspace...";
		case "done":
			return "Ready to go!";
		case "error":
			return "Something went wrong";
		case "idle":
			return "Setting up your account...";
	}
}

export function Onboarding() {
	const { data: whoAmIResponse } = useQuery(whoAmI, {});
	const user = whoAmIResponse?.user;
	const { autoCreate, step, error, shouldAutoCreate, isLoadingOrgs, hasOrgs } = useAutoCreateOrgWorkspace();
	const navigate = useNavigate();

	useEffect(() => {
		if (!user || isLoadingOrgs) {
			return;
		}

		if (hasOrgs) {
			void navigate("/dashboard");
			return;
		}

		if (!shouldAutoCreate || autoCreateStarted) {
			return;
		}

		autoCreateStarted = true;

		autoCreate(user.email)
			.then((result) => {
				setTimeout(() => {
					if (result.orgId && result.workspaceId) {
						void navigate(workspacePath(result.orgId, result.workspaceId));
					}
				}, 500);
			})
			.catch(() => undefined);
	}, [user, autoCreate, shouldAutoCreate, hasOrgs, isLoadingOrgs, navigate]);

	const failed = error !== null;
	useAppLoader(!failed, user === undefined ? undefined : stepLabel(step));

	if (!failed) {
		return null;
	}

	return (
		<AuthStatusScreen title="We couldn't finish setting up your account" description={error}>
			<Button
				onClick={() => {
					window.location.reload();
				}}
			>
				Try again
			</Button>
		</AuthStatusScreen>
	);
}
