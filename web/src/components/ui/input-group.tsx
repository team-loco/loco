import * as React from "react"

import { cn } from "@/lib/utils"

const InputGroup = ({ ref, className, ...props }: React.HTMLAttributes<HTMLDivElement> & { ref?: React.RefObject<HTMLDivElement | null> }) => (
	<div
		ref={ref}
		className={cn("relative flex items-center gap-0", className)}
		{...props}
	/>
)
InputGroup.displayName = "InputGroup"

const InputGroupInput = ({ ref, className, ...props }: React.InputHTMLAttributes<HTMLInputElement> & { ref?: React.RefObject<HTMLInputElement | null> }) => (
	<input
		ref={ref}
		className={cn(
			"flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-base ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
			className
		)}
		{...props}
	/>
)
InputGroupInput.displayName = "InputGroupInput"

interface InputGroupAddonProps extends React.HTMLAttributes<HTMLDivElement> {
	align?: "inline-start" | "inline-end"
}

const InputGroupAddon = ({ ref, className, align = "inline-start", ...props }: InputGroupAddonProps & { ref?: React.RefObject<HTMLDivElement | null> }) => (
		<div
			ref={ref}
			className={cn(
				"absolute flex items-center gap-1 px-3 pointer-events-none text-muted-foreground",
				align === "inline-start" && "left-0",
				align === "inline-end" && "right-0",
				className
			)}
			{...props}
		/>
	)
InputGroupAddon.displayName = "InputGroupAddon"

export { InputGroup, InputGroupInput, InputGroupAddon }
