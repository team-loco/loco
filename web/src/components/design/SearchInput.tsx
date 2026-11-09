import { SearchIcon, XIcon } from "lucide-react";

import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/design/InputGroup";
import { cn } from "@/lib/utils";

function SearchInput({
	className,
	onClear,
	...props
}: React.ComponentProps<typeof InputGroupInput> & { onClear?: () => void }) {
	return (
		<InputGroup
			className={cn(
				"h-[30px] has-[[data-slot=input-group-control]:focus-visible]:ring-0 has-[>[data-align=inline-start]]:[&>input]:pl-2",
				className,
			)}
		>
			<InputGroupAddon>
				<SearchIcon />
			</InputGroupAddon>
			<InputGroupInput className="pr-2 text-[13px] md:text-[13px]" {...props} />
			{onClear !== undefined && props.value !== "" && (
				<InputGroupAddon align="inline-end" className="pr-1.5">
					<button
						type="button"
						aria-label="Clear search"
						onClick={onClear}
						className="flex size-5 items-center justify-center rounded-sm hover:text-foreground"
					>
						<XIcon className="size-3" />
					</button>
				</InputGroupAddon>
			)}
		</InputGroup>
	);
}

export { SearchInput };
