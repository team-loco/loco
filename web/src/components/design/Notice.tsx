import { CircleXIcon, InfoIcon, LockIcon, TriangleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Alert } from "@/components/ui/alert";
import { cn } from "@/lib/utils";

export type NoticeTone = "info" | "warn" | "bad" | "lock";

const TONE: Record<NoticeTone, { className: string; Icon: typeof InfoIcon }> = {
	info: { className: "[--tn:var(--info)]", Icon: InfoIcon },
	warn: {
		className:
			"[--tn:var(--warn)] border-[color-mix(in_oklch,var(--warn)_24%,var(--line))] bg-[color-mix(in_oklch,var(--warn)_5%,var(--bg))]",
		Icon: TriangleAlertIcon,
	},
	bad: {
		className:
			"[--tn:var(--err)] border-[color-mix(in_oklch,var(--err)_24%,var(--line))] bg-[color-mix(in_oklch,var(--err)_5%,var(--bg))]",
		Icon: CircleXIcon,
	},
	lock: { className: "[--tn:var(--fg3)]", Icon: LockIcon },
};

const inlineCode =
	"[&_code]:rounded-[4px] [&_code]:bg-background [&_code]:box-decoration-clone [&_code]:px-[5px] [&_code]:py-px [&_code]:font-mono [&_code]:text-[0.92em] [&_code]:text-foreground [&_code]:shadow-[inset_0_0_0_1px_var(--line)]";

/** One notice for every tone: the icon carries the tone, the text stays in the foreground colour. */
export function Notice({
	id,
	tone = "info",
	role,
	title,
	detail,
	note,
	action,
	className,
	children,
}: {
	id?: string | undefined;
	tone?: NoticeTone;
	role?: "alert" | "note" | "status";
	title?: ReactNode;
	detail?: ReactNode;
	note?: ReactNode;
	action?: ReactNode;
	className?: string | undefined;
	children?: ReactNode;
}) {
	const { className: toneClass, Icon } = TONE[tone];
	const titled = title !== undefined;
	return (
		<Alert
			id={id}
			data-slot="notice"
			data-tone={tone}
			role={role ?? (tone === "bad" ? "alert" : "note")}
			className={cn(
				"grid-cols-[16px_minmax(0,1fr)_auto] items-center gap-0 gap-x-[11px] rounded-lg border-line bg-panel py-2.5 pr-2.5 pl-[13px] text-[13.5px] leading-[1.45] text-foreground max-[560px]:grid-cols-[16px_minmax(0,1fr)]",
				toneClass,
				inlineCode,
				className,
			)}
		>
			<span data-slot="notice-icon" aria-hidden className={cn("flex text-(--tn)", titled && "mt-0.5 self-start")}>
				<Icon className="size-4" />
			</span>
			<div className="flex min-w-0 flex-col gap-0.5">
				{titled && (
					<div data-slot="notice-title" className="font-medium text-foreground">
						{title}
					</div>
				)}
				{children !== undefined && <div className={cn(titled && "text-fg2")}>{children}</div>}
				{detail !== undefined && (
					<div className="mt-0.5 font-mono text-[11.5px] text-fg3 [overflow-wrap:anywhere]">{detail}</div>
				)}
				{note !== undefined && <div className="text-[12.5px] text-fg3">{note}</div>}
			</div>
			{action !== undefined && (
				<div
					className={cn(
						"ml-1.5 flex items-center gap-1.5 max-[560px]:col-start-2 max-[560px]:mt-2 max-[560px]:ml-0",
						titled && "self-start",
					)}
				>
					{action}
				</div>
			)}
		</Alert>
	);
}
