import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";

export const PHASE_COLOR_MAP: Record<DeploymentPhase, string> = {
	[DeploymentPhase.UNSPECIFIED]: "bg-[#ede7dd] text-[#7a6a58]",
	[DeploymentPhase.PENDING]:     "bg-[#ede7dd] text-[#7a6a58]",
	[DeploymentPhase.DEPLOYING]:   "bg-[#e8edf8] text-[#3a5298]",
	[DeploymentPhase.RUNNING]:     "bg-[#eaf2ed] text-[#3a6b4a]",
	[DeploymentPhase.SUCCEEDED]:   "bg-[#eaf2ed] text-[#3a6b4a]",
	[DeploymentPhase.FAILED]:      "bg-[#fdeaea] text-[#8b2e2e]",
	[DeploymentPhase.CANCELED]:    "bg-[#f0ece6] text-[#8a7a68]",
};

export const BADGE_COLOR_MAP: Record<DeploymentPhase, string> = {
	[DeploymentPhase.UNSPECIFIED]: "bg-[#7a6a58] text-white",
	[DeploymentPhase.PENDING]:     "bg-[#7a6a58] text-white",
	[DeploymentPhase.DEPLOYING]:   "bg-[#3a5298] text-white",
	[DeploymentPhase.RUNNING]:     "bg-[#3a6b4a] text-white",
	[DeploymentPhase.SUCCEEDED]:   "bg-[#3a6b4a] text-white",
	[DeploymentPhase.FAILED]:      "bg-[#8b2e2e] text-white",
	[DeploymentPhase.CANCELED]:    "bg-[#8a7a68] text-white",
};
