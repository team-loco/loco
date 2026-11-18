import { Avatar, AvatarFallback, AvatarImage } from "@/components/design/Avatar";
import { cn } from "@/lib/utils";

const NAME_SEPARATORS = /[\s@._-]+/;

export function initialsOf(name: string, email: string): string {
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
	return (
		<Avatar className={cn("size-7", className)}>
			{avatarUrl !== "" && <AvatarImage src={avatarUrl} alt="" />}
			<AvatarFallback>{initialsOf(name, email)}</AvatarFallback>
		</Avatar>
	);
}
