import { SearchIcon } from "lucide-react";

import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/design/InputGroup";
import { cn } from "@/lib/utils";

function SearchInput({ className, ...props }: React.ComponentProps<typeof InputGroupInput>) {
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
		</InputGroup>
	);
}

export { SearchInput };
