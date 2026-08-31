import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";
import type { ResourceStatusLabel } from "@/lib/app-status";

export interface StatusCfg {
	dot: string;
	color: string;
	bg: string;
	label: string;
}

export type StatusKey =
	| "healthy"
	| "deploying"
	| "degraded"
	| "failed"
	| "suspended"
	| "pending";

export const STATUS_CFG: Record<StatusKey, StatusCfg> = {
	healthy: {
		dot: "#4a7c59",
		color: "#3a6b4a",
		bg: "#eaf2ed",
		label: "Healthy",
	},
	deploying: {
		dot: "#5b7ec0",
		color: "#3a5298",
		bg: "#e8edf8",
		label: "Deploying",
	},
	degraded: {
		dot: "#d4870a",
		color: "#9c6b1e",
		bg: "#fdf3e3",
		label: "Degraded",
	},
	failed: {
		dot: "#c0392b",
		color: "#8b2e2e",
		bg: "#fdeaea",
		label: "Unavailable",
	},
	suspended: {
		dot: "#b0a090",
		color: "#7a6a58",
		bg: "#f0ece6",
		label: "Suspended",
	},
	pending: {
		dot: "#b0a090",
		color: "#7a6a58",
		bg: "#f0ece6",
		label: "Pending",
	},
};

export interface PhaseCfg {
	label: string;
	bg: string;
	color: string;
}

export const PHASE_CFG: Record<DeploymentPhase, PhaseCfg> = {
	[DeploymentPhase.UNSPECIFIED]: {
		label: "Unknown",
		bg: "#ede7dd",
		color: "#7a6a58",
	},
	[DeploymentPhase.PENDING]: {
		label: "Pending",
		bg: "#ede7dd",
		color: "#7a6a58",
	},
	[DeploymentPhase.DEPLOYING]: {
		label: "Deploying",
		bg: "#e8edf8",
		color: "#3a5298",
	},
	[DeploymentPhase.RUNNING]: {
		label: "Running",
		bg: "#eaf2ed",
		color: "#3a6b4a",
	},
	[DeploymentPhase.SUCCEEDED]: {
		label: "Succeeded",
		bg: "#eaf2ed",
		color: "#3a6b4a",
	},
	[DeploymentPhase.FAILED]: {
		label: "Failed",
		bg: "#fdeaea",
		color: "#8b2e2e",
	},
	[DeploymentPhase.CANCELED]: {
		label: "Canceled",
		bg: "#f0ece6",
		color: "#8a7a68",
	},
};


export function statusKeyFromLabel(label: ResourceStatusLabel): StatusKey {
	if (label === "running") return "healthy";
	if (label === "unavailable") return "failed";
	return label;
}
