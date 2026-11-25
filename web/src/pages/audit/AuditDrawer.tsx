import { useState, type ReactNode } from "react";
import { toJsonString } from "@bufbuild/protobuf";
import { EventSchema } from "@gen/loco/event/v1/event_pb";
import { ChevronDownIcon, ChevronUpIcon, XIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/design/DropdownMenu";
import { RowMenu } from "@/components/design/RowMenu";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { useCopy } from "@/hooks/useCopy";
import { formatFullTime } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { PlainCode } from "@/pages/observability/shared";

import type { AuditRow } from "./model";

function Field({ term, children, mono = true }: { term: string; children: ReactNode; mono?: boolean }) {
	return (
		<div className="grid grid-cols-[110px_minmax(0,1fr)] items-baseline gap-x-4 py-1.5">
			<dt className="text-fg3">{term}</dt>
			<dd className={`m-0 min-w-0 [overflow-wrap:anywhere] ${mono ? "font-mono text-[12.5px]" : ""}`}>{children}</dd>
		</div>
	);
}

export function AuditDrawer({
	open,
	row,
	onNewer,
	onOlder,
	onResource,
	onActor,
	onClose,
}: {
	open: boolean;
	row: AuditRow | undefined;
	onNewer: (() => void) | null;
	onOlder: (() => void) | null;
	onResource: (() => void) | null;
	onActor: () => void;
	onClose: () => void;
}) {
	const timePref = useTimePreferences();
	const [wrap, setWrap] = useState(true);
	const [copied, copy] = useCopy();
	const raw = row === undefined ? "" : toJsonString(EventSchema, row.event, { prettySpaces: 2 });
	return (
		<Sheet
			open={open}
			onOpenChange={(next) => {
				if (!next) onClose();
			}}
		>
			<SheetContent size="wide" showCloseButton={false}>
				{row !== undefined && (
					<>
						<SheetHeader className="items-start pr-4">
							<div className="flex min-w-0 flex-1 flex-col gap-1.5">
								<SheetTitle className="truncate text-[20px] font-normal tracking-[-0.02em]">{row.label}</SheetTitle>
								<SheetDescription className="truncate font-mono text-[12px] text-fg3">{row.detail}</SheetDescription>
							</div>
							<div className="flex shrink-0 items-center gap-0.5">
								<RowMenu label="More for this event">
									<DropdownMenuItem onClick={onActor}>More by {row.actor.name}</DropdownMenuItem>
									{onResource !== null && (
										<DropdownMenuItem onClick={onResource}>More for {row.resource}</DropdownMenuItem>
									)}
									<DropdownMenuSeparator />
									{row.event.requestId !== "" && (
										<DropdownMenuItem
											onClick={() => {
												copy("request", row.event.requestId, "Copied request ID");
											}}
										>
											Copy request ID
										</DropdownMenuItem>
									)}
									<DropdownMenuItem
										onClick={() => {
											copy("json", raw, "Copied event as JSON");
										}}
									>
										Copy as JSON
									</DropdownMenuItem>
								</RowMenu>
								<Button
									variant="ghost"
									size="icon"
									aria-label="Newer"
									disabled={onNewer === null}
									onClick={onNewer ?? undefined}
									className="text-fg2"
								>
									<ChevronUpIcon />
								</Button>
								<Button
									variant="ghost"
									size="icon"
									aria-label="Older"
									disabled={onOlder === null}
									onClick={onOlder ?? undefined}
									className="text-fg2"
								>
									<ChevronDownIcon />
								</Button>
								<Button variant="ghost" size="icon" aria-label="Close" onClick={onClose} className="text-fg2">
									<XIcon />
								</Button>
							</div>
						</SheetHeader>
						<SheetBody>
							<dl className="m-0 text-[13px]">
								<Field term="Time">{formatFullTime(row.ms, false, timePref)}</Field>
								<Field term="Actor" mono={false}>
									{row.actor.name}
									<span className="text-[11.5px] text-fg3">
										{" "}
										{row.actorKind}
										{row.actor.detail !== "" && row.actor.detail !== row.actorKind && (
											<span className="font-mono">, {row.actor.detail}</span>
										)}
									</span>
								</Field>
								<Field term="Event">{row.event.type}</Field>
								<Field term="Where">{row.where}</Field>
								<Field term="Environment">{row.environment === "" ? "—" : row.environment}</Field>
								<Field term="Request ID">{row.event.requestId === "" ? "—" : row.event.requestId}</Field>
								<Field term="Sequence">#{row.event.seq.toLocaleString()}</Field>
							</dl>
							<div className="flex flex-col gap-1.5">
								<span className="text-[12.5px] text-fg3">Raw event</span>
								<PlainCode
									copied={copied === "raw"}
									copyLabel="Copy JSON"
									onCopy={() => {
										copy("raw", raw);
									}}
									wrap={wrap}
									onWrap={setWrap}
								>
									{raw}
								</PlainCode>
							</div>
						</SheetBody>
					</>
				)}
			</SheetContent>
		</Sheet>
	);
}
