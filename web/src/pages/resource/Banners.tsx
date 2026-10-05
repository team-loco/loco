import { InfoIcon, OctagonAlertIcon, TriangleAlertIcon, XIcon } from "lucide-react";
import { Link } from "react-router";

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
				className="flex size-[26px] cursor-pointer items-center justify-center rounded-sm border-0 bg-transparent text-inherit hover:bg-black/5"
			>
				<XIcon className="size-4" />
			</button>
		</div>
	);
}

export function ErrorBanner({
	title,
	message,
	logsHref,
	onEvents,
}: {
	title: string;
	message: string;
	logsHref: string | undefined;
	onEvents: () => void;
}) {
	return (
		<div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3.5 rounded-lg border border-bad-fg bg-bad-bg px-4 py-3 text-bad-fg">
			<OctagonAlertIcon className="size-[18px]" />
			<div className="flex min-w-0 flex-col gap-0.5">
				<span className="font-semibold">{title}</span>
				<span className="truncate" title={message}>
					{message}
				</span>
			</div>
			<div className="flex gap-2">
				{logsHref !== undefined && (
					<Link
						to={logsHref}
						className="flex h-7 items-center rounded-sm border border-bad-fg px-2.5 text-bad-fg hover:bg-black/5 hover:no-underline"
					>
						Logs
					</Link>
				)}
				<button
					type="button"
					onClick={onEvents}
					className="h-7 cursor-pointer rounded-sm border border-bad-fg bg-transparent px-2.5 text-bad-fg hover:bg-black/5"
				>
					Events
				</button>
			</div>
		</div>
	);
}
