import { DeploymentPhase } from "@/gen/deployment/v1/deployment_pb";

export const PHASE_COLOR_MAP: Record<DeploymentPhase, string> = {
	[DeploymentPhase.UNSPECIFIED]:
		"bg-gray-100 text-gray-800 dark:bg-gray-900 dark:text-gray-200",
	[DeploymentPhase.PENDING]:
		"bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-200",
	[DeploymentPhase.DEPLOYING]:
		"bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-200",
	[DeploymentPhase.RUNNING]:
		"bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200",
	[DeploymentPhase.SUCCEEDED]:
		"bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200",
	[DeploymentPhase.FAILED]:
		"bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200",
	[DeploymentPhase.CANCELED]: "bg-red-400 text-destructive-foreground",
};
