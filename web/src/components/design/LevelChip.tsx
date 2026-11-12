import { LEVEL_LABEL, LEVEL_TEXT, type AccessLevel } from "@/hooks/useMyScopes";
import { cn } from "@/lib/utils";

export function LevelChip({
	level,
	name,
	nameFirst = false,
	title,
}: {
	level: AccessLevel;
	name: string;
	nameFirst?: boolean;
	title?: string;
}) {
	const levelText = <span className={cn("font-medium", LEVEL_TEXT[level])}>{LEVEL_LABEL[level]}</span>;
	const nameText = <span className="truncate font-mono text-[11.5px]">{name}</span>;
	return (
		<span
			title={title}
			className="inline-flex h-6 max-w-full items-center gap-1.5 rounded-md bg-panel2 px-2 text-[12px] whitespace-nowrap text-fg2"
		>
			{nameFirst ? nameText : levelText}
			{nameFirst ? levelText : nameText}
		</span>
	);
}
