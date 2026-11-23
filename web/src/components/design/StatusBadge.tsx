import { DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { Badge } from "@/components/ui/badge";
import type { Rollout } from "@/lib/resource-status";
import { cn } from "@/lib/utils";

import type { Glyph } from "./StatusGlyph";

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
			return { label: "Completed", glyph: "done" };
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

type Tone = "ok" | "done" | "info" | "warn" | "err" | "neutral";

const TONE: Record<Glyph, Tone> = {
	ok: "ok",
	done: "done",
	spin: "info",
	warn: "warn",
	err: "err",
	off: "neutral",
	draft: "neutral",
};

/** A tinted fill with tone text, like the level badge; a completed deployment is a quiet gray with a thin ring. */
const TONE_CLASS: Record<Tone, string> = {
	ok: "[--tn:var(--ok)]",
	info: "[--tn:var(--info)]",
	warn: "[--tn:var(--warn)]",
	err: "[--tn:var(--err)]",
	neutral: "[--tn:var(--fg4)]",
	done: "[--tn:var(--fg)] bg-fg/7 text-fg2 shadow-[inset_0_0_0_1px_color-mix(in_oklch,var(--ok)_35%,var(--line2))]",
};

export function StatusBadge({ glyph, label, className }: StatusStyle & { className?: string | undefined }) {
	const tone = TONE[glyph];
	return (
		<Badge
			variant="outline"
			data-status-badge={tone}
			className={cn(
				"h-5 justify-start gap-0 rounded-[5px] border-0 bg-[color-mix(in_oklch,var(--tn)_13%,transparent)] px-[7px] py-0 text-[12px] font-medium text-[color:color-mix(in_oklch,var(--tn)_72%,var(--fg))]",
				TONE_CLASS[tone],
				className,
			)}
		>
			{label}
		</Badge>
	);
}

export function ResourceStatusBadge({ status }: { status: ResourceStatus | undefined }) {
	return <StatusBadge {...resourceStatusStyle(status)} />;
}

export const ROLLOUT_STYLE: Record<Rollout, StatusStyle & { short: string }> = {
	updating: { label: "Updating", short: "Updating", glyph: "spin" },
	failed: { label: "Update failed, previous version still serving", short: "Update failed", glyph: "err" },
};

export function RolloutBadge({ rollout, compact = false }: { rollout: Rollout | undefined; compact?: boolean }) {
	if (rollout === undefined) return null;
	const { label, short, glyph } = ROLLOUT_STYLE[rollout];
	return <StatusBadge glyph={glyph} label={compact ? short : label} />;
}

const SUPERSEDED: StatusStyle = { label: "Completed", glyph: "done" };

export function DeploymentStatusBadge({
	phase,
	superseded = false,
}: {
	phase: DeploymentStatus | undefined;
	superseded?: boolean;
}) {
	return <StatusBadge {...(superseded ? SUPERSEDED : deploymentPhaseStyle(phase))} />;
}
