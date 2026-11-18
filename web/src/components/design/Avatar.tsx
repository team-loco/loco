import {
	Avatar as AvatarBase,
	AvatarBadge,
	AvatarFallback as AvatarFallbackBase,
	AvatarGroup,
	AvatarGroupCount,
	AvatarImage,
} from "@/components/ui/avatar";
import { cn } from "@/lib/utils";

function Avatar({ className, ...props }: React.ComponentProps<typeof AvatarBase>) {
	return <AvatarBase className={cn("size-8 font-mono text-[10px] after:hidden", className)} {...props} />;
}

function AvatarFallback({ className, ...props }: React.ComponentProps<typeof AvatarFallbackBase>) {
	return <AvatarFallbackBase className={cn("bg-panel2 text-[length:inherit] text-fg2", className)} {...props} />;
}

export { Avatar, AvatarBadge, AvatarFallback, AvatarGroup, AvatarGroupCount, AvatarImage };
