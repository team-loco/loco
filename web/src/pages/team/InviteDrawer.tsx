import { useMutation } from "@connectrpc/connect-query";
import { inviteOrgMember } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { OrgRole } from "@gen/loco/org/v1/org_pb";
import { useState } from "react";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { CopyButton } from "@/components/design/CopyButton";
import { Field } from "@/components/design/Field";
import { Sheet, SheetBody, SheetContent, SheetFooter, SheetHeader, SheetTitle } from "@/components/design/Sheet";
import { StatusGlyph } from "@/components/design/StatusGlyph";
import { Textarea } from "@/components/design/Textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { pluralize } from "@/lib/format";
import { cn } from "@/lib/utils";

import { inviteFailure, parseEmails, ROLE_HINT, type InviteOutcome, type Role } from "./model";
import { InlineCode } from "@/components/design/InlineCode";

const ROLE_OPTIONS = [
	{ value: "Member", label: "Member" },
	{ value: "Admin", label: "Admin" },
] as const;

const TEXTAREA_ROWS = 3;

export function InviteDrawer({
	orgId,
	orgName,
	open,
	onClose,
	onInvited,
}: {
	orgId: string;
	orgName: string;
	open: boolean;
	onClose: () => void;
	onInvited: () => void;
}) {
	return (
		<Sheet
			open={open}
			onOpenChange={(next) => {
				if (!next) onClose();
			}}
		>
			<SheetContent size="wide">
				<SheetHeader>
					<SheetTitle>
						Invite to <InlineCode>{orgName}</InlineCode>
					</SheetTitle>
				</SheetHeader>
				{open && <InviteForm orgId={orgId} orgName={orgName} onClose={onClose} onInvited={onInvited} />}
			</SheetContent>
		</Sheet>
	);
}

function InviteForm({
	orgId,
	orgName,
	onClose,
	onInvited,
}: {
	orgId: string;
	orgName: string;
	onClose: () => void;
	onInvited: () => void;
}) {
	const [text, setText] = useState("");
	const [role, setRole] = useState<Role>("Member");
	const [results, setResults] = useState<InviteOutcome[] | null>(null);
	const mutation = useMutation(inviteOrgMember);
	const [sending, setSending] = useState(false);
	const chips = parseEmails(text);
	const invalid = chips.filter((c) => !c.valid).length;
	const blocked = chips.length === 0 || invalid > 0 || sending;

	const send = async () => {
		if (blocked) return;
		setSending(true);
		const outcomes = await Promise.all(
			chips.map(async ({ value }): Promise<InviteOutcome> => {
				try {
					const res = await mutation.mutateAsync({
						orgId,
						email: value,
						role: role === "Admin" ? OrgRole.ADMIN : OrgRole.MEMBER,
					});
					if (res.invitation === undefined) return inviteFailure(value, null);
					return { kind: "invited", email: value, invitation: res.invitation };
				} catch (err) {
					return inviteFailure(value, err);
				}
			}),
		);
		setSending(false);
		setResults(outcomes);
		onInvited();
	};

	if (results !== null) {
		return (
			<>
				<SheetBody>
					<ul aria-live="polite" className="m-0 flex list-none flex-col gap-2 p-0">
						{results.map((r) => (
							<OutcomeRow key={r.email} outcome={r} />
						))}
					</ul>
				</SheetBody>
				<SheetFooter>
					<Button onClick={onClose}>Done</Button>
				</SheetFooter>
			</>
		);
	}

	const hint =
		invalid > 0 ? (
			`${pluralize(invalid, "address")} ${invalid === 1 ? "isn't" : "aren't"} valid`
		) : (
			<>
				Separate several with commas, spaces or new lines. They join <InlineCode>{orgName}</InlineCode> with the role
				below when they first sign in.
			</>
		);

	return (
		<form
			className="contents"
			onSubmit={(e) => {
				e.preventDefault();
				void send();
			}}
		>
			<SheetBody>
				<Field label="Emails" hint={invalid > 0 ? undefined : hint} error={invalid > 0 ? hint : undefined}>
					<Textarea
						autoFocus
						rows={TEXTAREA_ROWS}
						value={text}
						placeholder="ana@example.com, lee@example.com"
						aria-invalid={invalid > 0}
						className="font-mono"
						onChange={(e) => {
							setText(e.target.value);
						}}
					/>
				</Field>
				<Field label="Role" hint={ROLE_HINT[role]}>
					<div>
						<ToggleGroup
							variant="segmented"
							aria-label="Role"
							className="w-fit"
							value={[role]}
							onValueChange={(next: string[]) => {
								const picked = next[0];
								if (picked !== undefined) setRole(picked === "Admin" ? "Admin" : "Member");
							}}
						>
							{ROLE_OPTIONS.map((o) => (
								<ToggleGroupItem key={o.value} value={o.value}>
									{o.label}
								</ToggleGroupItem>
							))}
						</ToggleGroup>
					</div>
				</Field>
				{chips.length > 0 && (
					<ul aria-label="Addresses" className="m-0 flex list-none flex-wrap gap-1.5 p-0">
						{chips.map((c) => (
							<li
								key={c.value}
								data-chip
								className={cn(
									"inline-flex h-6 items-center rounded-md px-2 font-mono text-[11.5px]",
									c.valid ? "bg-panel2 text-fg2" : "bg-bad-bg text-bad-fg",
								)}
							>
								{c.value}
								{!c.valid && <span className="sr-only"> (not a valid address)</span>}
							</li>
						))}
					</ul>
				)}
			</SheetBody>
			<SheetFooter>
				<Button type="button" variant="outline" onClick={onClose}>
					Cancel
				</Button>
				<Button type="submit" disabled={blocked}>
					{sending ? "Sending…" : chips.length > 1 ? `Send ${chips.length.toString()} invites` : "Send invite"}
				</Button>
			</SheetFooter>
		</form>
	);
}

function OutcomeRow({ outcome }: { outcome: InviteOutcome }) {
	const invitation = outcome.kind === "invited" ? outcome.invitation : undefined;
	const link = invitation?.link;
	const detail = outcome.kind === "invited" ? "" : outcome.message;
	return (
		<li className="flex items-start gap-2.5 rounded-lg bg-panel px-3 py-2.5">
			<span className="mt-0.5">
				{outcome.kind === "invited" && <StatusGlyph glyph="ok" />}
				{outcome.kind === "exists" && <StatusGlyph glyph="off" />}
				{outcome.kind === "failed" && <StatusGlyph glyph="err" />}
			</span>
			<span className="flex min-w-0 flex-1 flex-col gap-1">
				<span className="flex flex-wrap items-center gap-2">
					<span className="truncate font-mono text-[12.5px]">{outcome.email}</span>
					{invitation !== undefined && (
						<Badge size="sm" tone="info">
							Invited
						</Badge>
					)}
					{outcome.kind === "exists" && (
						<Badge size="sm" tone="muted">
							Already a member or invited
						</Badge>
					)}
					{outcome.kind === "failed" && (
						<Badge size="sm" tone="bad">
							Not invited
						</Badge>
					)}
				</span>
				{detail !== "" && (
					<span className={cn("text-[12.5px]", outcome.kind === "exists" ? "text-fg3" : "text-bad-fg")}>{detail}</span>
				)}
				{link !== undefined && (
					<span className="flex items-center gap-2">
						<span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-fg3">{link}</span>
						<CopyButton text={link} label="Copy link" />
					</span>
				)}
			</span>
		</li>
	);
}
