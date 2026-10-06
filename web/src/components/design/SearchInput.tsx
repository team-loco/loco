import { SearchIcon } from "lucide-react"

import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/design/InputGroup"
import { cn } from "@/lib/utils"

function SearchInput({
	className,
	size = "default",
	...props
}: Omit<React.ComponentProps<typeof InputGroupInput>, "size"> & {
	size?: "default" | "sm" | undefined
}) {
	const sm = size === "sm"
	return (
		<InputGroup
			className={cn(
				sm
					? "h-[30px] has-[>[data-align=inline-start]]:[&>input]:pl-1.5"
					: "h-[34px] rounded-lg",
				className
			)}
		>
			<InputGroupAddon className={cn(sm && "data-[align=inline-start]:pl-2")}>
				<SearchIcon />
			</InputGroupAddon>
			<InputGroupInput className={cn(sm && "pr-2 text-[12.5px] md:text-[12.5px]")} {...props} />
		</InputGroup>
	)
}

export { SearchInput }
