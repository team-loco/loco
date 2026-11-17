import type { ReactElement } from "react";
import { Link } from "react-router";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { orgSettingsPath } from "@/lib/routes";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/design/Tooltip";
import { type LimitTone, type LimitUsage, limitTone, limitPillLabel, limitReason, usageLabel } from "@/lib/limits";
import { cn } from "@/lib/utils";
import "./LimitedAction.css";

export const LIMIT_TONE_TEXT: Record<LimitTone, string> = { warn: "text-warn-fg", bad: "text-bad-fg" };

/** "used / max" in amber or red from 90% of the limit; nothing below it. */
export function LimitUsageText({ usage, className }: { usage: LimitUsage | null; className?: string }) {
	const tone = limitTone(usage);
	if (usage === null || tone === null) return null;
	return (
		<span className={cn("font-mono text-[12px] tabular-nums", LIMIT_TONE_TEXT[tone], className)}>
			{usageLabel(usage)}
		</span>
	);
}

export function LimitCount({ usage }: { usage: LimitUsage | null }) {
	const { activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const tone = limitTone(usage);
	if (usage === null || tone === null) return null;
	const reason = limitReason(usage);
	return (
		<Link
			className={cn("lim-pill", tone === "bad" ? "full" : "near")}
			to={activeOrgId === null ? "/organizations" : `${orgSettingsPath(activeOrgId, activeWorkspaceId)}#s-lim`}
			title={reason ?? undefined}
		>
			{limitPillLabel(usage)}
			<span className="lim-go" aria-hidden>
				→
			</span>
		</Link>
	);
}

/** A disabled create action explains its limit in a tooltip. */
export function LimitedAction({ reason, children }: { reason: string | null; children: ReactElement }) {
	if (reason === null) return children;
	return (
		<Tooltip>
			<TooltipTrigger render={<span className="inline-flex" />}>{children}</TooltipTrigger>
			<TooltipContent className="max-w-[280px]">{reason}</TooltipContent>
		</Tooltip>
	);
}
