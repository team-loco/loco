import { Avatar, AvatarFallback, AvatarImage } from "@/components/design/Avatar";
import { cn } from "@/lib/utils";

const AV_BG = ["oklch(0.9 0.05 250)", "oklch(0.9 0.06 160)", "oklch(0.9 0.07 60)", "oklch(0.9 0.06 320)", "oklch(0.9 0.05 200)", "oklch(0.9 0.07 30)"];
const AV_FG = ["oklch(0.4 0.12 250)", "oklch(0.4 0.1 160)", "oklch(0.42 0.1 60)", "oklch(0.4 0.12 320)", "oklch(0.4 0.1 200)", "oklch(0.42 0.12 30)"];

const NAME_SEPARATORS = /[\s@._-]+/;

function initialsOf(name: string, email: string): string {
	const source = name.trim() !== "" ? name : email;
	const parts = source.split(NAME_SEPARATORS).filter((s) => s !== "");
	return parts
		.map((s) => s.charAt(0))
		.join("")
		.slice(0, 2)
		.toUpperCase();
}

export function MemberAvatar({
	name,
	email,
	avatarUrl,
	className,
}: {
	name: string;
	email: string;
	avatarUrl: string;
	className?: string | undefined;
}) {
	const key = email || name;
	const i = (key.charCodeAt(0) + key.length) % AV_BG.length;
	const initials = initialsOf(name, email);
	return (
		<Avatar className={cn("size-7 after:hidden", className)}>
			{avatarUrl !== "" && <AvatarImage src={avatarUrl} alt="" />}
			<AvatarFallback
				className="text-[11px] font-semibold"
				style={{ background: AV_BG[i] ?? AV_BG[0], color: AV_FG[i] ?? AV_FG[0] }}
			>
				{initials}
			</AvatarFallback>
		</Avatar>
	);
}
