import { PlusIcon } from "lucide-react";

import { initialsOf } from "./MemberAvatar";
import "./TeamArt.css";

const SLOTS = [0, 1, 2];

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
			{SLOTS.map((i) => (
				<button
					key={i}
					type="button"
					aria-label="Invite people"
					disabled={disabled}
					onClick={onInvite}
					className="team-art-slot flex size-11 items-center justify-center rounded-full border border-dashed border-fg3 text-fg3 hover:border-foreground hover:text-foreground disabled:animate-none disabled:hover:border-fg3 disabled:hover:text-fg3"
				>
					<PlusIcon className="size-3.5" />
				</button>
			))}
		</div>
	);
}
