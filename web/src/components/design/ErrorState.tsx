import type { DescMethod } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { CheckIcon, CopyIcon, LockIcon, MonitorIcon, RotateCwIcon, ServerIcon, XIcon } from "lucide-react";
import { useState, useSyncExternalStore, type ReactNode } from "react";

import { useAuth } from "@/auth/AuthProvider";
import { BASE_URL } from "@/auth/connect-transport";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { orgMembersOptions } from "@/hooks/completeMembers";
import { useCopy } from "@/hooks/useCopy";
import { useMyScopes } from "@/hooks/useMyScopes";
import { errorKind, requestId } from "@/lib/error-handler";
import { userLabel } from "@/lib/user";
import { MemberAvatar } from "@/pages/team/MemberAvatar";
import { roleOf } from "@/pages/team/model";

import { Badge } from "./Badge";
import { Button } from "./Button";
import { CopyId } from "./CopyId";
import { EmptyHero } from "./EmptyHero";
import { Spinner } from "./Spinner";

/** What the caller is missing when the load is denied: the workspace, an admin role, or the whole organization. */
export type DeniedScope = "workspace" | "role" | "org";

const RATE_RETRY_S = 12;
const RING_R = 26;
const RING_C = 2 * Math.PI * RING_R;
const API_HOST = new URL(BASE_URL).host;

// Errors keep the time they were first shown, so a failed retry reports its own time.
const shownAt = new WeakMap<object, Date>();
function firstShown(error: unknown): Date {
	if (typeof error !== "object" || error === null) return new Date();
	const at = shownAt.get(error) ?? new Date();
	shownAt.set(error, at);
	return at;
}

function codeName(error: unknown): string {
	const code = error instanceof ConnectError ? error.code : Code.Unknown;
	return Code[code].replace(/([a-z])([A-Z])/g, "$1_$2").toLowerCase();
}

function RetryButton({ onRetry }: { onRetry: () => Promise<unknown> }) {
	const [busy, setBusy] = useState(false);
	return (
		<Button
			disabled={busy}
			onClick={() => {
				setBusy(true);
				void onRetry().finally(() => {
					setBusy(false);
				});
			}}
		>
			{busy ? <Spinner /> : <RotateCwIcon />}
			{busy ? "Retrying" : "Retry"}
		</Button>
	);
}

function Unavailable({ onRetry, className }: { onRetry: () => Promise<unknown>; className: string | undefined }) {
	const end =
		"inline-flex h-8 items-center gap-[7px] rounded-lg border border-line bg-background px-[11px] [&_svg]:text-fg3";
	return (
		<EmptyHero
			role="alert"
			className={className}
			art={
				<div className="mb-[18px] flex items-center text-[12.5px] text-fg2">
					<span className={end}>
						<MonitorIcon className="size-3.5" />
						This browser
					</span>
					<span className="relative mx-1.5 h-0.5 w-[120px] animate-wire-flow bg-[repeating-linear-gradient(90deg,var(--line2)_0_6px,transparent_6px_11px)] bg-size-[22px_2px] motion-reduce:animate-none">
						<span className="absolute top-1/2 left-1/2 flex size-5 -translate-1/2 items-center justify-center rounded-full border border-[color-mix(in_oklch,var(--err)_45%,var(--line))] bg-background text-err">
							<XIcon className="size-[11px]" />
						</span>
					</span>
					<span className={`${end} font-mono`}>
						<ServerIcon className="size-3.5" />
						{API_HOST}
					</span>
				</div>
			}
			title="Can’t reach Loco"
			subtitle="Your apps keep running. Check your connection and try again."
		>
			<RetryButton onRetry={onRetry} />
		</EmptyHero>
	);
}

function Internal({
	error,
	what,
	rpc,
	onRetry,
	className,
}: {
	error: unknown;
	what: string;
	rpc: string;
	onRetry: () => Promise<unknown>;
	className: string | undefined;
}) {
	const [copied, copy] = useCopy(1400);
	const id = requestId(error);
	const at = firstShown(error);
	const code = codeName(error);
	const details = [
		...(id === null ? [] : [`Request ID: ${id}`]),
		`Call: ${rpc}`,
		`Code: ${code}`,
		`Time: ${at.toISOString()}`,
		`Page: ${window.location.href}`,
	].join("\n");
	const row = (key: string, value: ReactNode) => (
		<div className="flex min-w-0 gap-3.5 py-1 not-first:border-t not-first:border-dashed not-first:border-line">
			<span className="w-14 shrink-0 text-fg3">{key}</span>
			{value}
		</div>
	);
	const text = (value: string, tone = "text-foreground") => <span className={`min-w-0 truncate ${tone}`}>{value}</span>;
	return (
		<EmptyHero
			role="alert"
			className={className}
			art={
				<div className="mx-auto mb-[18px] flex w-[min(440px,100%)] flex-col rounded-[10px] border border-line bg-background px-3.5 py-2.5 text-left font-mono text-[12px]">
					{id !== null &&
						row(
							"request",
							<CopyId
								value={id}
								title="Copy request ID"
								className="min-w-0 justify-start p-0 font-mono text-[12px] text-foreground hover:bg-transparent hover:underline hover:decoration-fg3 hover:underline-offset-3"
							>
								<span className="min-w-0 truncate">{id}</span>
							</CopyId>,
						)}
					{row("call", text(rpc))}
					{row("code", text(code, "text-err"))}
					{row("at", text(at.toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" })))}
				</div>
			}
			title={`Couldn’t load ${what}`}
			subtitle="Something failed on Loco’s side. If retrying doesn’t help, send support the details."
		>
			<div className="mt-1.5 flex flex-wrap justify-center gap-2">
				<RetryButton onRetry={onRetry} />
				<Button
					variant="outline"
					onClick={() => {
						copy("details", details);
					}}
				>
					{copied === null ? <CopyIcon className="text-fg3" /> : <CheckIcon className="text-fg3" />}
					{copied === null ? "Copy details" : "Copied"}
				</Button>
			</div>
		</EmptyHero>
	);
}

function Denied({ scope, className }: { scope: DeniedScope; className: string | undefined }) {
	const transport = useTransport();
	const { user } = useAuth();
	const { activeOrgId, activeWorkspaceId, orgs, workspaces } = useOrgWorkspace();
	const { orgLevel } = useMyScopes();
	const orgId = activeOrgId ?? "";
	const members = useQuery({ ...orgMembersOptions(transport, orgId), enabled: orgId !== "" && scope !== "org" });
	const org = orgs.find((o) => o.id === activeOrgId)?.name ?? "this organization";
	const ws = workspaces.find((w) => w.id === activeWorkspaceId)?.name ?? "this workspace";
	const admins = (members.data?.members ?? []).flatMap((m) =>
		m.user !== undefined && roleOf(m.scopes) === "Admin" ? [m.user] : [],
	);
	const copy = {
		workspace: {
			title: `You don’t have access to ${ws}`,
			body: `You’re in ${org}, but not in this workspace. An admin can add you.`,
		},
		role: { title: `Only admins can change ${org} settings`, body: "You’re a Member. An admin can change your role." },
		org: { title: `You’re not a member of ${org}`, body: `Ask someone in ${org} to invite you.` },
	}[scope];
	return (
		<EmptyHero
			role="alert"
			className={className}
			art={
				<div className="mb-[18px] flex flex-col items-center gap-3">
					<div className="flex items-center gap-2 rounded-full border border-line bg-background py-1.5 pr-2.5 pl-1.5 text-[13px]">
						<MemberAvatar
							name={user?.name ?? ""}
							email={user?.email ?? ""}
							avatarUrl={user?.avatarUrl ?? ""}
							className="size-6 rounded-full [&_*]:rounded-full [&_[data-slot=avatar-fallback]]:text-[10px]"
						/>
						You
						{scope !== "org" && <Badge size="sm">{orgLevel(orgId) >= 3 ? "Admin" : "Member"}</Badge>}
						<LockIcon className="ml-0.5 size-[13px] text-err" />
					</div>
					{admins.length > 0 && (
						<div className="flex flex-wrap items-center justify-center gap-2.5 text-[12px] text-fg2">
							<span className="text-fg3">Admins</span>
							{admins.map((a) => (
								<a
									key={a.id}
									href={`mailto:${a.email}`}
									title={a.email}
									className="inline-flex items-center gap-1.5 rounded-full py-0.5 pr-2 pl-0.5 no-underline hover:bg-panel2 hover:text-foreground"
								>
									<MemberAvatar
										name={a.name}
										email={a.email}
										avatarUrl={a.avatarUrl}
										className="size-6 rounded-full [&_*]:rounded-full [&_[data-slot=avatar-fallback]]:text-[10px]"
									/>
									{userLabel(a)}
								</a>
							))}
						</div>
					)}
				</div>
			}
			title={copy.title}
			subtitle={copy.body}
		/>
	);
}

class Countdown {
	left = RATE_RETRY_S;
	constructor(private readonly onDone: () => Promise<unknown>) {}
	subscribe = (notify: () => void) => {
		const id = setInterval(() => {
			if (this.left === 0) return;
			this.left -= 1;
			notify();
			if (this.left > 0) return;
			void this.onDone().finally(() => {
				this.left = RATE_RETRY_S;
				notify();
			});
		}, 1000);
		return () => {
			clearInterval(id);
		};
	};
	getSnapshot = () => this.left;
}

function RateLimited({ onRetry, className }: { onRetry: () => Promise<unknown>; className: string | undefined }) {
	const [countdown] = useState(() => new Countdown(onRetry));
	const left = useSyncExternalStore(countdown.subscribe, countdown.getSnapshot);
	return (
		<EmptyHero
			role="alert"
			className={className}
			art={
				<div className="relative mx-auto mb-[18px] size-16">
					<svg width={64} height={64} viewBox="0 0 64 64" aria-hidden>
						<circle cx={32} cy={32} r={RING_R} fill="none" stroke="var(--line)" strokeWidth={3} />
						<circle
							cx={32}
							cy={32}
							r={RING_R}
							fill="none"
							stroke="var(--warn)"
							strokeWidth={3}
							strokeLinecap="round"
							strokeDasharray={RING_C}
							strokeDashoffset={RING_C * (1 - left / RATE_RETRY_S)}
							transform="rotate(-90 32 32)"
							className="transition-[stroke-dashoffset] duration-1000 ease-linear motion-reduce:transition-none"
						/>
					</svg>
					<span className="absolute inset-0 flex items-center justify-center font-mono text-[13px]">
						{left === 0 ? "…" : `${left.toString()}s`}
					</span>
				</div>
			}
			title="Too many requests"
			subtitle="Loco is limiting requests from your account for a moment. This page retries on its own."
		/>
	);
}

/** Replaces a page body whose main query failed; `rpc` names that query in the support details. */
export function ErrorState({
	error,
	what,
	rpc,
	scope = "workspace",
	onRetry,
	className,
}: {
	error: unknown;
	what: string;
	rpc: DescMethod;
	scope?: DeniedScope;
	onRetry: () => Promise<unknown>;
	className?: string;
}) {
	const kind = errorKind(error);
	switch (kind) {
		case null:
			return null;
		case "unavailable":
			return <Unavailable onRetry={onRetry} className={className} />;
		case "internal":
			return (
				<Internal
					error={error}
					what={what}
					rpc={`${rpc.parent.name}/${rpc.name}`}
					onRetry={onRetry}
					className={className}
				/>
			);
		case "denied":
			return <Denied scope={scope} className={className} />;
		case "rate":
			return <RateLimited onRetry={onRetry} className={className} />;
	}
}
