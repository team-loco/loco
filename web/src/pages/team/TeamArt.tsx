import { PlusIcon } from "lucide-react";

import { initialsOf } from "./MemberAvatar";

const SLOT_DELAYS = ["0s", "0.3s", "0.6s"];

export function TeamArt({
	name,
	email,
	disabled,
	onInvite,
}: {
	name: string;
	email: string;
	disabled: boolean;
	onInvite: () => void;
}) {
	return (
		<div className="flex justify-center gap-2.5">
			<span className="flex size-11 items-center justify-center rounded-full bg-foreground text-[13px] font-medium text-background">
				{initialsOf(name, email)}
			</span>
			{SLOT_DELAYS.map((delay) => (
				<button
					key={delay}
					type="button"
					aria-label="Invite people"
					disabled={disabled}
					onClick={onInvite}
					style={{ animationDelay: delay }}
					className="flex size-11 items-center justify-center rounded-full border border-dashed border-fg3 text-fg3 hover:border-foreground hover:text-foreground motion-safe:animate-[team-art-slot_3s_ease-in-out_infinite_both] disabled:animate-none disabled:hover:border-fg3 disabled:hover:text-fg3"
				>
					<PlusIcon className="size-3.5" />
				</button>
			))}
		</div>
	);
}
