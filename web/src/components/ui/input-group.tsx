import * as React from "react"

import { cn } from "@/lib/utils"

const InputGroup = React.forwardRef<
	HTMLDivElement,
	React.HTMLAttributes<HTMLDivElement>
>(({ className, ...props }, ref) => (
	<div
		ref={ref}
		className={cn("relative flex items-center gap-0", className)}
		{...props}
	/>
))
InputGroup.displayName = "InputGroup"

const InputGroupInput = React.forwardRef<
	HTMLInputElement,
	React.InputHTMLAttributes<HTMLInputElement>
>(({ className, ...props }, ref) => (
	<input
		ref={ref}
		className={cn(
			"flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-base ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
			className
		)}
		{...props}
	/>
))
InputGroupInput.displayName = "InputGroupInput"

interface InputGroupAddonProps extends React.HTMLAttributes<HTMLDivElement> {
	align?: "inline-start" | "inline-end"
}

const InputGroupAddon = React.forwardRef<HTMLDivElement, InputGroupAddonProps>(
	({ className, align = "inline-start", ...props }, ref) => (
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
)
InputGroupAddon.displayName = "InputGroupAddon"

export { InputGroup, InputGroupInput, InputGroupAddon }
