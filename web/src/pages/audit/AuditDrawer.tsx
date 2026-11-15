import { useTimePreferences } from "@/lib/use-time-preferences";
import { formatFullTime } from "@/lib/time";
import { toJsonString } from "@bufbuild/protobuf";
import { EventSchema } from "@gen/loco/event/v1/event_pb";
import { ChevronDownIcon, ChevronUpIcon } from "lucide-react";
import type { ReactNode } from "react";

import { Button } from "@/components/design/Button";
import { CodeBlock } from "@/components/design/CodeBlock";
import { CopyId } from "@/components/design/CopyId";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/design/Sheet";
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
	open,
	row,
	nowMs,
	onNewer,
	onOlder,
	onResource,
	onActor,
	onClose,
}: {
	open: boolean;
	row: AuditRow | undefined;
	nowMs: number;
	onNewer: (() => void) | null;
	onOlder: (() => void) | null;
	onResource: (() => void) | null;
	onActor: () => void;
	onClose: () => void;
}) {
	const timePref = useTimePreferences();
	return (
		<Sheet
			open={open}
			onOpenChange={(next) => {
				if (!next) onClose();
			}}
		>
			<SheetContent size="wide">
				{row !== undefined && (
					<>
						<SheetHeader className="pr-[140px]">
							<div className="flex min-w-0 flex-col gap-0.5">
								<SheetTitle className="truncate">{row.label}</SheetTitle>
								<SheetDescription className="truncate font-mono text-[12.5px]">{row.detail}</SheetDescription>
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
									<span>{formatFullTime(row.ms, false, timePref)}</span>
									<span className="text-[11.5px] text-fg3">{agoLabel(row.ms, nowMs)}</span>
								</Item>
								<Item term="Actor">
									<span>{row.actor.name}</span>
									<span className="text-[11.5px] text-fg3">
										{row.actorKind}
										{row.actor.detail !== "" && <span className="font-mono"> · {row.actor.detail}</span>}
									</span>
								</Item>
								<Item term="Event">
									<span className="font-mono text-[12.5px]">{row.event.type}</span>
								</Item>
								<Item term="Where">
									<span className="font-mono text-[12.5px]">{row.where}</span>
								</Item>
								<Item term="Environment">
									<span className="font-mono text-[12.5px]">{row.environment === "" ? "—" : row.environment}</span>
								</Item>
								<Item term="Request">
									{row.event.requestId === "" ? (
										"—"
									) : (
										<CopyId
											value={row.event.requestId}
											title="Copy request ID"
											className="w-fit max-w-full justify-start p-0 font-mono text-[12.5px] font-normal hover:bg-transparent hover:underline"
										>
											<span className="min-w-0 truncate">{row.event.requestId}</span>
										</CopyId>
									)}
								</Item>
								<Item term="Sequence">
									<span className="font-mono text-[12.5px]">#{row.event.seq.toLocaleString()}</span>
								</Item>
							</dl>
							<div>
								<Button variant="outline" onClick={onActor}>
									More by {row.actor.name}
								</Button>
							</div>
							{onResource !== null && (
								<div>
									<Button variant="outline" onClick={onResource}>
										More for {row.resource}
									</Button>
								</div>
							)}
							<div className="flex flex-col gap-1.5">
								<span className="text-[12.5px] text-fg3">Raw event</span>
								<CodeBlock language="json" className="max-h-[320px]">
									{toJsonString(EventSchema, row.event, { prettySpaces: 2 })}
								</CodeBlock>
							</div>
						</SheetBody>
					</>
				)}
			</SheetContent>
		</Sheet>
	);
}
