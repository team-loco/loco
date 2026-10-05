import { useQuery } from "@connectrpc/connect-query";
import { Check } from "lucide-react";
import { useEffect, useRef } from "react";
import { useNavigate } from "react-router";

import { whoAmI } from "@gen/loco/user/v1/user-UserService_connectquery";

import { AuthStatusScreen } from "@/components/AuthStatusScreen";
import { Button } from "@/components/design/Button";
import { Progress } from "@/components/design/Progress";
import { useAutoCreateOrgWorkspace } from "@/hooks/useAutoCreateOrgWorkspace";
import { workspacePath } from "@/lib/routes";
import { cn } from "@/lib/utils";

type OnboardingStep = ReturnType<typeof useAutoCreateOrgWorkspace>["step"];

const STEPS = [
	{ label: "Creating organization", value: 33 },
	{ label: "Creating workspace", value: 66 },
	{ label: "Setting up your account", value: 100 },
] as const;

function progressValue(step: OnboardingStep): number {
	switch (step) {
		case "creating-org":
			return 33;
		case "creating-workspace":
			return 66;
		case "done":
			return 100;
		case "error":
			return 0;
		case "idle":
			return 0;
	}
}

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
	const hasStarted = useRef(false);
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

		if (!shouldAutoCreate || hasStarted.current) {
			return;
		}

		hasStarted.current = true;

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

	if (!user) {
		return null;
	}

	const value = progressValue(step);
	const label = stepLabel(step);

	return (
		<AuthStatusScreen title="Welcome to Loco" description="Setting up your account...">
			<div className="flex flex-col gap-4">
				<div className="flex flex-col gap-2">
					<span className="font-medium">{label}</span>
					<Progress value={value} aria-label={label} />
				</div>

				{error !== null && (
					<div role="alert" className="flex flex-col items-start gap-2 rounded-sm bg-bad-bg px-3 py-2.5 text-bad-fg">
						<span className="text-sm">Error: {error}</span>
						<Button
							variant="link"
							className="text-sm text-bad-fg hover:text-bad-fg"
							onClick={() => {
								window.location.reload();
							}}
						>
							Try again
						</Button>
					</div>
				)}

				<ul className="m-0 flex list-none flex-col gap-2 p-0 text-sm">
					{STEPS.map((stepItem) => {
						const complete = value >= stepItem.value;
						return (
							<li
								key={stepItem.label}
								className={cn("flex items-center gap-2", complete ? "text-foreground" : "text-fg3")}
							>
								<span
									className={cn(
										"flex size-4 items-center justify-center rounded-full border",
										complete ? "border-primary bg-primary text-primary-foreground" : "border-line2",
									)}
								>
									{complete && <Check className="size-2.5" strokeWidth={3} />}
								</span>
								{stepItem.label}
							</li>
						);
					})}
				</ul>
			</div>
		</AuthStatusScreen>
	);
}
