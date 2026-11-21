import { DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { Badge } from "@/components/ui/badge";
import type { Rollout } from "@/lib/resource-status";
import { cn } from "@/lib/utils";

import { type Glyph, StatusGlyph } from "./StatusGlyph";

export interface StatusStyle {
	label: string;
	glyph: Glyph;
}

export function resourceStatusStyle(status: ResourceStatus | undefined): StatusStyle {
	switch (status) {
		case ResourceStatus.HEALTHY:
			return { label: "Healthy", glyph: "ok" };
		case ResourceStatus.DEPLOYING:
			return { label: "Deploying", glyph: "spin" };
		case ResourceStatus.DEGRADED:
			return { label: "Degraded", glyph: "warn" };
		case ResourceStatus.UNAVAILABLE:
			return { label: "Unavailable", glyph: "err" };
		case ResourceStatus.UNKNOWN:
			return { label: "Unknown", glyph: "off" };
		case ResourceStatus.UNSPECIFIED:
			return { label: "Pending", glyph: "draft" };
		case undefined:
			return { label: "Pending", glyph: "draft" };
	}
}

export function deploymentPhaseStyle(phase: DeploymentStatus | undefined): StatusStyle {
	switch (phase) {
		case DeploymentStatus.BUILDING:
			return { label: "Building", glyph: "draft" };
		case DeploymentStatus.DEPLOYING:
			return { label: "Deploying", glyph: "spin" };
		case DeploymentStatus.RUNNING:
			return { label: "Running", glyph: "ok" };
		case DeploymentStatus.DEGRADED:
			return { label: "Degraded", glyph: "warn" };
		case DeploymentStatus.CRASHED:
			return { label: "Crashed", glyph: "err" };
		case DeploymentStatus.SUPERSEDED:
			return { label: "Superseded", glyph: "off" };
		case DeploymentStatus.FAILED:
			return { label: "Failed", glyph: "err" };
		case DeploymentStatus.CANCELED:
			return { label: "Canceled", glyph: "off" };
		case DeploymentStatus.UNSPECIFIED:
			return { label: "Pending", glyph: "draft" };
		case undefined:
			return { label: "Pending", glyph: "draft" };
	}
}

type Tone = "ok" | "info" | "warn" | "err" | "neutral";

const TONE: Record<Glyph, Tone> = {
	ok: "ok",
	spin: "info",
	warn: "warn",
	err: "err",
	off: "neutral",
	draft: "neutral",
};

const TONE_VAR: Record<Tone, string> = {
	ok: "[--tn:var(--ok)]",
	info: "[--tn:var(--info)]",
	warn: "[--tn:var(--warn)]",
	err: "[--tn:var(--err)]",
	neutral: "[--tn:var(--fg4)]",
};

export function StatusBadge({ glyph, label, className }: StatusStyle & { className?: string | undefined }) {
	const tone = TONE[glyph];
	return (
		<Badge
			variant="outline"
			data-status-badge={tone}
			className={cn(
				"h-6 justify-start gap-1.5 rounded-[5px] border-[color-mix(in_oklch,var(--tn)_28%,transparent)] bg-[color-mix(in_oklch,var(--tn)_12%,var(--bg))] py-0 pr-[9px] pl-1.5 text-[12.5px] font-medium text-foreground",
				TONE_VAR[tone],
				className,
			)}
		>
			<StatusGlyph glyph={glyph} className="size-[13px]" />
			{label}
		</Badge>
	);
}

export function ResourceStatusBadge({ status }: { status: ResourceStatus | undefined }) {
	return <StatusBadge {...resourceStatusStyle(status)} />;
}

export const ROLLOUT_STYLE: Record<Rollout, StatusStyle> = {
	updating: { label: "Updating", glyph: "spin" },
	failed: { label: "Update failed · previous version serving", glyph: "err" },
};

export function RolloutBadge({ rollout }: { rollout: Rollout | undefined }) {
	return rollout === undefined ? null : <StatusBadge {...ROLLOUT_STYLE[rollout]} />;
}

const SUPERSEDED: StatusStyle = { label: "Superseded", glyph: "off" };

export function DeploymentStatusBadge({
	phase,
	superseded = false,
}: {
	phase: DeploymentStatus | undefined;
	superseded?: boolean;
}) {
	return <StatusBadge {...(superseded ? SUPERSEDED : deploymentPhaseStyle(phase))} />;
}
