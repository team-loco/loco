import {
	Avatar as AvatarBase,
	AvatarBadge,
	AvatarFallback as AvatarFallbackBase,
	AvatarGroup,
	AvatarGroupCount,
	AvatarImage,
} from "@/components/ui/avatar"
import { cn } from "@/lib/utils"

function Avatar({ className, ...props }: React.ComponentProps<typeof AvatarBase>) {
	return <AvatarBase className={cn("size-8 rounded-lg after:rounded-lg [&_img]:rounded-lg", className)} {...props} />
}

function AvatarFallback({ className, ...props }: React.ComponentProps<typeof AvatarFallbackBase>) {
	return (
		<AvatarFallbackBase
			className={cn("rounded-lg bg-line text-sm font-semibold text-foreground", className)}
			{...props}
		/>
	)
}

export { Avatar, AvatarBadge, AvatarFallback, AvatarGroup, AvatarGroupCount, AvatarImage }
