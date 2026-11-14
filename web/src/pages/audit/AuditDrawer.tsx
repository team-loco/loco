import { toJsonString } from "@bufbuild/protobuf";
import { EventSchema } from "@gen/loco/event/v1/event_pb";
import { ChevronDownIcon, ChevronUpIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/design/Button";
import { CopyId } from "@/components/design/CopyId";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { useHeldWhileClosed } from "@/hooks/useHeldWhileClosed";
import { agoLabel } from "@/lib/time";

import type { AuditRow } from "./model";

function Item({ term, children }: { term: string; children: ReactNode }) {
	return (
		<>
			<dt className="text-fg3">{term}</dt>
			<dd className="m-0 flex min-w-0 flex-col gap-0.5 [overflow-wrap:anywhere]">{children}</dd>
		</>
	);
}

export function AuditDrawer({
	row,
	nowMs,
	onNewer,
	onOlder,
	onResource,
	onClose,
}: {
	row: AuditRow | undefined;
	nowMs: number;
	onNewer: (() => void) | null;
	onOlder: (() => void) | null;
	onResource: (() => void) | null;
	onClose: () => void;
}) {
	const shown = useHeldWhileClosed(row, row !== undefined);
	return (
		<Sheet
			open={row !== undefined}
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
		>
			<SheetContent size="wide">
				{shown !== undefined && (
					<>
						<SheetHeader className="pr-[140px]">
							<div className="flex min-w-0 flex-col gap-0.5">
								<SheetTitle className="truncate">{shown.label}</SheetTitle>
								<SheetDescription className="truncate font-mono text-[12.5px]">{shown.detail}</SheetDescription>
							</div>
							<div className="absolute top-[17px] right-12 flex gap-1">
								<Button
									variant="ghost"
									size="icon-sm"
									aria-label="Newer event"
									disabled={onNewer === null}
									onClick={onNewer ?? undefined}
								>
									<ChevronUpIcon />
								</Button>
								<Button
									variant="ghost"
									size="icon-sm"
									aria-label="Older event"
									disabled={onOlder === null}
									onClick={onOlder ?? undefined}
								>
									<ChevronDownIcon />
								</Button>
							</div>
						</SheetHeader>
						<SheetBody>
							<dl className="m-0 grid grid-cols-[110px_minmax(0,1fr)] gap-x-4 gap-y-3 text-[13px]">
								<Item term="When">
									<span>{new Date(shown.ms).toLocaleString([], { dateStyle: "medium", timeStyle: "medium" })}</span>
									<span className="text-[11.5px] text-fg3">{agoLabel(shown.ms, nowMs)}</span>
								</Item>
								<Item term="Actor">
									<span>{shown.actor.name}</span>
									<span className="text-[11.5px] text-fg3">
										{shown.actorKind}
										{shown.actor.detail !== "" && <span className="font-mono"> · {shown.actor.detail}</span>}
									</span>
								</Item>
								<Item term="Event">
									<span className="font-mono text-[12.5px]">{shown.event.type}</span>
								</Item>
								<Item term="Where">
									<span className="font-mono text-[12.5px]">{shown.where}</span>
								</Item>
								<Item term="Environment">
									<span className="font-mono text-[12.5px]">{shown.environment === "" ? "—" : shown.environment}</span>
								</Item>
								<Item term="Request">
									{shown.event.requestId === "" ? (
										"—"
									) : (
										<CopyId
											value={shown.event.requestId}
											title="Copy request ID"
											className="w-fit max-w-full justify-start p-0 font-mono text-[12.5px] font-normal hover:bg-transparent hover:underline"
										>
											<span className="min-w-0 truncate">{shown.event.requestId}</span>
										</CopyId>
									)}
								</Item>
								<Item term="Sequence">
									<span className="font-mono text-[12.5px]">#{shown.event.seq.toLocaleString()}</span>
								</Item>
							</dl>
							{onResource !== null && (
								<div>
									<Button variant="outline" onClick={onResource}>
										More for {shown.resource}
									</Button>
								</div>
							)}
							<div className="flex flex-col gap-1.5">
								<span className="text-[12.5px] text-fg3">Raw event</span>
								<pre className="m-0 max-h-[320px] overflow-auto rounded-lg border border-line bg-panel px-3.5 py-3 font-mono text-[12px] leading-[1.55]">
									{toJsonString(EventSchema, shown.event, { prettySpaces: 2 })}
								</pre>
							</div>
						</SheetBody>
					</>
				)}
			</SheetContent>
		</Sheet>
	);
}
