import { createQueryOptions, useMutation, useTransport } from "@connectrpc/connect-query";
import { listSessions, revokeSession } from "@gen/loco/user/v1/user-UserService_connectquery";
import { SessionKind, type ListSessionsResponse, type Session } from "@gen/loco/user/v1/user_pb";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { MonitorIcon, TerminalIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { ConfirmDialog } from "@/components/design/ConfirmDialog";
import { Skeleton } from "@/components/design/Skeleton";
import { getErrorMessage } from "@/lib/error-handler";
import { formatHourMinute, formatMonthDayYear, maybeTsMs, type TimePreferences } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";

import { SettingsCard } from "./parts";

const browsers = [
	[/Edg(?:A|iOS)?\//, "Edge"],
	[/(?:Firefox|FxiOS)\//, "Firefox"],
	[/(?:Chrome|CriOS)\//, "Chrome"],
	[/Safari\//, "Safari"],
] as const;
const devices = [
	[/iPhone/, "iPhone"],
	[/iPad/, "iPad"],
	[/Android/, "Android"],
	[/Windows/, "Windows"],
	[/Macintosh/, "macOS"],
	[/Linux/, "Linux"],
] as const;

function sessionLabel(session: Session): string {
	if (session.kind === SessionKind.CLI) return "Loco CLI";

	const browser = browsers.find(([pattern]) => pattern.test(session.userAgent))?.[1] ?? "Dashboard";
	const device = devices.find(([pattern]) => pattern.test(session.userAgent))?.[1];
	return device === undefined ? browser : `${browser} on ${device}`;
}

function SessionDate({ value, label, pref }: { value: Session["lastUsedAt"]; label: string; pref: TimePreferences }) {
	const ms = maybeTsMs(value);
	if (ms === undefined) return null;
	return (
		<span>
			{label}{" "}
			<time dateTime={new Date(ms).toISOString()}>
				{formatMonthDayYear(ms, pref)}, {formatHourMinute(ms, pref)}
			</time>
		</span>
	);
}

export function ActiveSessions() {
	const pref = useTimePreferences();
	const transport = useTransport();
	const queryClient = useQueryClient();
	const options = createQueryOptions(listSessions, {}, { transport });
	const sessions = useQuery({
		...options,
		meta: { persist: false },
		staleTime: 0,
		refetchOnWindowFocus: true,
	});
	const [selected, setSelected] = useState<Session | null>(null);
	const revoke = useMutation(revokeSession);

	const signOut = async () => {
		if (selected === null || revoke.isPending) return;
		const sessionId = selected.id;
		try {
			await revoke.mutateAsync({ sessionId });
		} catch {
			return;
		}

		await queryClient.cancelQueries({ queryKey: options.queryKey, exact: true });
		queryClient.setQueryData<ListSessionsResponse>(options.queryKey, (data) =>
			data === undefined ? data : { ...data, sessions: data.sessions.filter((session) => session.id !== sessionId) },
		);
		setSelected(null);
		toast.success("Session signed out");
	};

	const rows = sessions.data?.sessions.toSorted((a, b) => Number(b.current) - Number(a.current)) ?? [];

	return (
		<SettingsCard className="mt-3">
			<div className="border-b border-line px-5 py-3.5">
				<h3 id="active-sessions-title" className="text-[13.5px] font-medium">
					Active sessions
				</h3>
				<p className="mt-0.5 text-[12.5px] text-fg3">Browsers and CLI sessions signed in to your account.</p>
			</div>
			{sessions.isPending ? (
				<div role="status" aria-label="Loading sessions" className="space-y-2 px-5 py-4">
					<Skeleton className="h-4 w-36" />
					<Skeleton className="h-3 w-48 max-w-full" />
				</div>
			) : sessions.isError && rows.length === 0 ? (
				<div className="flex flex-wrap items-center gap-3 px-5 py-3.5">
					<p className="flex-1 text-[13.5px] text-fg3" role="alert">
						{getErrorMessage(sessions.error, "Could not load active sessions")}
					</p>
					<Button variant="outline" size="sm" disabled={sessions.isFetching} onClick={() => void sessions.refetch()}>
						Retry
					</Button>
				</div>
			) : rows.length === 0 ? (
				<p className="px-5 py-3.5 text-[13.5px] text-fg3">No active sessions.</p>
			) : (
				<ul aria-labelledby="active-sessions-title" className="divide-y divide-line">
					{rows.map((session) => {
						const label = sessionLabel(session);
						const Icon = session.kind === SessionKind.CLI ? TerminalIcon : MonitorIcon;
						return (
							<li key={session.id} className="flex flex-wrap items-center gap-x-3 gap-y-2 px-5 py-3.5">
								<Icon className="size-4 shrink-0 text-fg3" aria-hidden="true" />
								<div className="flex min-w-[144px] flex-1 flex-col gap-0.5">
									<div className="flex flex-wrap items-center gap-x-2 gap-y-1">
										<span className="text-[13.5px] font-medium">{label}</span>
										{session.current && (
											<Badge size="sm" tone="info">
												This browser
											</Badge>
										)}
									</div>
									<div className="flex flex-wrap gap-x-3 gap-y-0.5 text-[12.5px] text-fg3">
										<SessionDate value={session.lastUsedAt} label="Last active" pref={pref} />
										<SessionDate value={session.createdAt} label="Signed in" pref={pref} />
									</div>
								</div>
								{!session.current && (
									<Button
										variant="outline"
										size="sm"
										aria-label={`Sign out ${label} session`}
										onClick={() => {
											revoke.reset();
											setSelected(session);
										}}
									>
										Sign out
									</Button>
								)}
							</li>
						);
					})}
				</ul>
			)}
			<ConfirmDialog
				open={selected !== null}
				title="Sign out this session?"
				description={
					selected === null ? null : `${sessionLabel(selected)} will need to sign in again to access your account.`
				}
				confirmLabel="Sign out"
				pendingLabel="Signing out…"
				pending={revoke.isPending}
				onConfirm={() => {
					void signOut();
				}}
				onClose={() => {
					setSelected(null);
				}}
			>
				{revoke.isError && (
					<p role="alert" className="text-[13.5px] text-bad-fg">
						{getErrorMessage(revoke.error, "Could not sign out this session")}
					</p>
				)}
			</ConfirmDialog>
		</SettingsCard>
	);
}
