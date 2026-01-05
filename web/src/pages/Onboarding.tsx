import { Card, CardContent } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { whoAmI } from "@/gen/user/v1";
import { useAutoCreateOrgWorkspace } from "@/hooks/useAutoCreateOrgWorkspace";
import { useQuery } from "@connectrpc/connect-query";
import { useEffect, useRef } from "react";
import { useNavigate } from "react-router";

export function Onboarding() {
	const navigate = useNavigate();
	const hasStarted = useRef(false);
	const { data: whoAmIResponse } = useQuery(whoAmI, {});
	const user = whoAmIResponse?.user;
	const { autoCreate, step, error, shouldAutoCreate } =
		useAutoCreateOrgWorkspace();

	useEffect(() => {
		if (!shouldAutoCreate || hasStarted.current || !user) {
			return;
		}

		hasStarted.current = true;

		// Start auto-creation
		autoCreate(user.email)
			.then(() => {
				// Wait a moment for smooth UX, then redirect
				setTimeout(() => {
					navigate("/dashboard");
				}, 500);
			})
			.catch(() => {
				// Error is handled in hook state
			});
	}, [user, navigate, autoCreate, shouldAutoCreate]);

	if (!user) {
		return null;
	}

	const steps = [
		{ label: "Creating organization", value: 33 },
		{ label: "Creating workspace", value: 66 },
		{ label: "Setting up your account", value: 100 },
	];

	const getProgressValue = () => {
		switch (step) {
			case "creating-org":
				return 33;
			case "creating-workspace":
				return 66;
			case "done":
				return 100;
			default:
				return 0;
		}
	};

	const getStepLabel = () => {
		switch (step) {
			case "creating-org":
				return "Creating your organization...";
			case "creating-workspace":
				return "Creating your workspace...";
			case "done":
				return "Ready to go!";
			case "error":
				return "Something went wrong";
			default:
				return "Setting up your account...";
		}
	};

	return (
		<div className="min-h-screen bg-background flex items-center justify-center px-6">
			<Card className="max-w-md w-full">
				<CardContent className="p-8">
					<div className="text-center mb-8">
						<div className="w-12 h-12 bg-main rounded-lg flex items-center justify-center text-white font-heading text-lg mx-auto mb-4">
							L
						</div>
						<h1 className="text-2xl font-heading text-foreground mb-2">
							Welcome to Loco
						</h1>
						<p className="text-sm text-muted-foreground">
							Setting up your account...
						</p>
					</div>

					<div className="space-y-4">
						<div>
							<p className="text-sm font-medium text-foreground mb-2">
								{getStepLabel()}
							</p>
							<Progress value={getProgressValue()} className="h-2" />
						</div>

						{error && (
							<div className="bg-red-50 border border-red-200 rounded p-3">
								<p className="text-sm text-red-700">Error: {error}</p>
								<button
									onClick={() => window.location.reload()}
									className="text-sm text-red-600 underline mt-2 hover:text-red-700"
								>
									Try again
								</button>
							</div>
						)}

						<div className="text-xs text-muted-foreground space-y-1">
							{steps.map((stepItem, idx) => (
								<div
									key={idx}
									className={`flex items-center gap-2 ${
										getProgressValue() >= stepItem.value
											? "text-foreground"
											: "text-muted-foreground"
									}`}
								>
									<div
										className={`w-4 h-4 rounded-full border border-border ${
											getProgressValue() >= stepItem.value
												? "bg-main text-white"
												: "bg-transparent"
										}`}
									/>
									<span>{stepItem.label}</span>
								</div>
							))}
						</div>
					</div>
				</CardContent>
			</Card>
		</div>
	);
}
