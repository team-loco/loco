import { DeploymentPhase } from "@/gen/loco/deployment/v1/deployment_pb";

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

export const BADGE_COLOR_MAP: Record<DeploymentPhase, string> = {
	[DeploymentPhase.UNSPECIFIED]:
		"bg-gray-700 text-gray-50 dark:bg-gray-600 dark:text-gray-100",
	[DeploymentPhase.PENDING]:
		"bg-yellow-600 text-yellow-50 dark:bg-yellow-500 dark:text-yellow-950",
	[DeploymentPhase.DEPLOYING]:
		"bg-yellow-600 text-yellow-50 dark:bg-yellow-500 dark:text-yellow-950",
	[DeploymentPhase.RUNNING]:
		"bg-blue-600 text-blue-50 dark:bg-blue-500 dark:text-blue-950",
	[DeploymentPhase.SUCCEEDED]:
		"bg-green-600 text-green-50 dark:bg-green-500 dark:text-green-950",
	[DeploymentPhase.FAILED]:
		"bg-red-600 text-red-50 dark:bg-red-500 dark:text-red-950",
	[DeploymentPhase.CANCELED]: "bg-red-700 text-red-50 dark:bg-red-600",
};
