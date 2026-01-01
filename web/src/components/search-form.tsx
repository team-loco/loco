import { Search } from "lucide-react";

import {
	InputGroup,
	InputGroupAddon,
	InputGroupInput,
} from "@/components/ui/input-group";
import { Kbd } from "@/components/ui/kbd";
import { Label } from "@/components/ui/label";

export function SearchForm({ ...props }: React.ComponentProps<"form">) {
	return (
		<form {...props}>
			<Label htmlFor="search" className="sr-only">
				Search
			</Label>
			<InputGroup>
				<InputGroupAddon align="inline-start">
					<Search className="size-3.5" />
				</InputGroupAddon>
				<InputGroupInput
					id="search"
					placeholder="Search..."
					className="h-8 px-7 pr-16 text-sm"
				/>
				<InputGroupAddon align="inline-end">
					<Kbd className="px-1.5 py-0.5 text-xs">⌘</Kbd>
					<Kbd className="px-1.5 py-0.5 text-xs">K</Kbd>
				</InputGroupAddon>
			</InputGroup>
		</form>
	);
}
