import { useTimePreferences } from "@/lib/use-time-preferences";
import { useMutation } from "@connectrpc/connect-query";
import { updateUser } from "@gen/loco/user/v1/user-UserService_connectquery";
import type { User } from "@gen/loco/user/v1/user_pb";
import { useQueryClient } from "@tanstack/react-query";
import { ChevronRightIcon, KeyRoundIcon, LogOutIcon } from "lucide-react";
import { Link } from "react-router";
import { toast } from "sonner";

import { useAuth } from "@/auth/AuthProvider";
import { Button } from "@/components/design/Button";
import { CopyId } from "@/components/design/CopyId";
import { Field } from "@/components/design/Field";
import { Input } from "@/components/design/Input";
import { SaveBar } from "@/components/design/SaveBar";
import { toastConnectError } from "@/lib/error-handler";
import { userLabel } from "@/lib/user";
import { MemberAvatar } from "@/pages/team/MemberAvatar";

import { DeleteAccount } from "./DeleteAccount";
import { DateTimeSection } from "./DateTimeSection";
import { AppearanceSection } from "./AppearanceSection";
import { ActiveSessions } from "./ActiveSessions";
import { formatDay, KeyValues, SettingsCard, SettingsSection } from "./parts";

const TOKENS_PATH = "/tokens?owner=personal";

export function PersonalTab({
	user,
	draft,
	onDraft,
}: {
	user: User;
	draft: string | null;
	onDraft: (name: string | null) => void;
}) {
	const timePref = useTimePreferences();
	const { logout } = useAuth();
	const queryClient = useQueryClient();
	const update = useMutation(updateUser);

	const name = draft ?? user.name;
	const nameT = name.trim();
	const dirty = name !== user.name;
	const nameErr = nameT === "" ? "Name required" : null;
	const shownErr = dirty ? nameErr : null;

	const save = () => {
		if (nameErr !== null || update.isPending) return;
		update.mutate(
			{ userId: user.id, name: nameT },
			{
				onSuccess: () => {
					onDraft(null);
					toast.success("Saved");
					void queryClient.invalidateQueries();
				},
				onError: (err) => {
					toastConnectError(err, "Failed to update your name");
				},
			},
		);
	};

	return (
		<div className="flex flex-col gap-[22px]">
			<SettingsSection title="Profile">
				<SettingsCard className="flex flex-col gap-4 px-5 py-[18px]">
					<div className="flex min-w-0 items-center gap-3.5">
						<MemberAvatar name={user.name} email={user.email} avatarUrl={user.avatarUrl} className="size-12" />
						<div className="flex min-w-0 flex-col gap-0.5">
							<span className="truncate text-[15px] font-medium">{userLabel(user)}</span>
							<span className="truncate font-mono text-[12px] text-fg3">{user.email}</span>
						</div>
					</div>
					<Field label="Name" error={shownErr}>
						<Input
							value={name}
							autoComplete="name"
							aria-invalid={shownErr !== null}
							className="h-9 max-w-[420px] text-[13.5px]"
							onChange={(e) => {
								onDraft(e.target.value);
							}}
						/>
					</Field>
					<KeyValues
						items={[
							[
								"ID",
								<CopyId
									key="id"
									value={user.id}
									className="h-6 max-w-full bg-panel2 px-2 font-mono text-[12px] font-normal"
								>
									<span className="truncate">{user.id}</span>
								</CopyId>,
							],
							["Joined", formatDay(user.createdAt, timePref)],
						]}
					/>
				</SettingsCard>
			</SettingsSection>

			<AppearanceSection />

			<DateTimeSection />

			<SettingsSection title="Access">
				<SettingsCard>
					<Link
						to={TOKENS_PATH}
						className="flex items-center gap-3 border-b border-line px-5 py-3.5 text-foreground no-underline hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none focus-visible:ring-inset"
					>
						<KeyRoundIcon className="size-4 shrink-0 text-fg3" />
						<span className="flex min-w-0 flex-1 flex-col gap-0.5">
							<span className="text-[13.5px] font-medium">Personal access tokens</span>
							<span className="text-[12.5px] text-fg3">Tokens that act as you, for the CLI and scripts.</span>
						</span>
						<ChevronRightIcon className="size-3.5 shrink-0 text-fg3" />
					</Link>
					<div className="flex flex-wrap items-center gap-3 px-5 py-3.5">
						<LogOutIcon className="size-4 shrink-0 text-fg3" />
						<span className="min-w-0 flex-1 text-[13.5px] font-medium">Log out</span>
						<Button
							variant="outline"
							size="sm"
							onClick={() => {
								void logout();
							}}
						>
							Log out
						</Button>
					</div>
				</SettingsCard>
				<ActiveSessions />
			</SettingsSection>

			<SettingsSection title="Danger zone">
				<DeleteAccount user={user} />
			</SettingsSection>

			{dirty && (
				<SaveBar
					onDiscard={() => {
						onDraft(null);
					}}
					onSave={save}
					saving={update.isPending}
					disabledReason={nameErr}
				/>
			)}
		</div>
	);
}
