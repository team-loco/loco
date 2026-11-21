import { createQueryOptions, useMutation, useTransport } from "@connectrpc/connect-query";
import { listSessions, revokeSession } from "@gen/loco/user/v1/user-UserService_connectquery";
import { SessionKind, type ListSessionsResponse } from "@gen/loco/user/v1/user_pb";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { MonitorIcon, TerminalIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Skeleton } from "@/components/design/Skeleton";
import { getErrorMessage } from "@/lib/error-handler";
import { useNow } from "@/hooks/useNow";
import { formatFullTime, formatMonthDay, maybeTsMs, type TimePreferences } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";

import { SettingsCard } from "./parts";
import { LogoutConfirm } from "./LogoutConfirm";
import { activeLabel, describeSession } from "./sessions";

function SessionTime({ ms, label, pref }: { ms: number; label: string; pref: TimePreferences }) {
	return (
		<time dateTime={new Date(ms).toISOString()} title={formatFullTime(ms, false, pref)}>
			{label}
		</time>
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
	});
	const nowMs = useNow(60_000).getTime();
	const [asking, setAsking] = useState<string | null>(null);
	const [loggingOut, setLoggingOut] = useState(false);
	const revoke = useMutation(revokeSession, {
		async onSuccess(_, { sessionId }) {
			await queryClient.cancelQueries({ queryKey: options.queryKey, exact: true });
			queryClient.setQueryData<ListSessionsResponse>(options.queryKey, (data) =>
				data === undefined ? data : { ...data, sessions: data.sessions.filter((session) => session.id !== sessionId) },
			);
			setAsking(null);
			toast.success("Session signed out");
		},
	});

	const rows = sessions.data?.sessions.toSorted((a, b) => Number(b.current) - Number(a.current)) ?? [];

	return (
		<SettingsCard className="mt-3">
			<div className="border-b border-line px-5 py-3.5">
				<div className="flex items-baseline gap-2">
					<h3 id="active-sessions-title" className="text-[13.5px] font-medium">
						Active sessions
					</h3>
					{sessions.isSuccess && <span className="font-mono text-[12px] text-fg3">{rows.length}</span>}
				</div>
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
						const { label, detail, ipAddress } = describeSession(session);
						const Icon = session.kind === SessionKind.CLI ? TerminalIcon : MonitorIcon;
						const lastUsed = maybeTsMs(session.lastUsedAt);
						const created = maybeTsMs(session.createdAt);
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
									{(detail !== "" || ipAddress !== "") && (
										<span className="text-[12.5px] text-fg3">{[detail, ipAddress].filter(Boolean).join(" · ")}</span>
									)}
									<div className="flex flex-wrap gap-x-3.5 gap-y-0.5 text-[12.5px] text-fg3">
										{lastUsed !== undefined && (
											<SessionTime ms={lastUsed} label={activeLabel(lastUsed, nowMs, pref)} pref={pref} />
										)}
										{created !== undefined && (
											<SessionTime ms={created} label={`Signed in ${formatMonthDay(created, pref)}`} pref={pref} />
										)}
									</div>
								</div>
								{session.current ? (
									<Button
										variant="outline"
										size="sm"
										onClick={() => {
											setLoggingOut(true);
										}}
									>
										Log out
									</Button>
								) : asking === session.id ? (
									<div
										role="group"
										aria-label="Confirm sign out"
										className="flex flex-wrap items-center gap-1.5 text-[12.5px] text-fg2"
									>
										<span className="mr-1">Sign out this session?</span>
										<Button
											variant="outline"
											size="sm"
											onClick={() => {
												setAsking(null);
											}}
										>
											Cancel
										</Button>
										<Button
											variant="destructive-outline"
											size="sm"
											disabled={revoke.isPending}
											onClick={() => {
												revoke.mutate({ sessionId: session.id });
											}}
										>
											{revoke.isPending ? "Signing out…" : "Sign out"}
										</Button>
										{revoke.isError && (
											<p role="alert" className="basis-full text-bad-fg">
												{getErrorMessage(revoke.error, "Could not sign out this session")}
											</p>
										)}
									</div>
								) : (
									<Button
										variant="outline"
										size="sm"
										aria-label={`Sign out ${label} session`}
										onClick={() => {
											revoke.reset();
											setAsking(session.id);
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
			<LogoutConfirm
				open={loggingOut}
				onClose={() => {
					setLoggingOut(false);
				}}
			/>
		</SettingsCard>
	);
}
