import { InfoIcon, OctagonAlertIcon, TriangleAlertIcon, XIcon } from "lucide-react";

import { cn } from "@/lib/utils";

import type { Notice } from "./model";

function toneClass(tone: Notice["tone"]): string {
	switch (tone) {
		case "info":
			return "border-info-fg bg-info-bg text-info-fg";
		case "warn":
			return "border-warn-fg bg-warn-bg text-warn-fg";
		case "bad":
			return "border-bad-fg bg-bad-bg text-bad-fg";
	}
}

function ToneIcon({ tone }: { tone: Notice["tone"] }) {
	switch (tone) {
		case "info":
			return <InfoIcon className="size-[18px]" />;
		case "warn":
			return <TriangleAlertIcon className="size-[18px]" />;
		case "bad":
			return <OctagonAlertIcon className="size-[18px]" />;
	}
}

export function NoticeBanner({ notice, onDismiss }: { notice: Notice; onDismiss: () => void }) {
	return (
		<div
			role="status"
			className={cn(
				"grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 rounded-lg border px-3.5 py-2.5",
				toneClass(notice.tone),
			)}
		>
			<span className="flex">
				<ToneIcon tone={notice.tone} />
			</span>
			<div className="flex min-w-0 flex-col gap-px">
				<span className="font-semibold">{notice.title}</span>
				<span className="truncate">{notice.message}</span>
			</div>
			<button
				type="button"
				aria-label="Dismiss"
				onClick={onDismiss}
				className="flex size-[26px] items-center justify-center rounded-sm border-0 bg-transparent text-inherit hover:bg-foreground/5"
			>
				<XIcon className="size-4" />
			</button>
		</div>
	);
}
