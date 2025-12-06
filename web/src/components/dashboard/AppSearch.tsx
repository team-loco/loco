import { Input } from "@/components/ui/input";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";

interface AppSearchProps {
	searchTerm: string;
	onSearchChange: (term: string) => void;
}

export function AppSearch({ searchTerm, onSearchChange }: AppSearchProps) {
	return (
		<div className="relative flex-1">
			<Input
				type="text"
				placeholder="Search apps..."
				value={searchTerm}
				onChange={(e) => onSearchChange(e.target.value)}
				className="w-full"
			/>
			{searchTerm && (
				<Button
					variant="secondary"
					size="sm"
					onClick={() => onSearchChange("")}
					className="absolute right-2 top-1/2 -translate-y-1/2 h-6 w-6 p-0"
					aria-label="Clear search"
				>
					<X className="w-4 h-4" />
				</Button>
			)}
		</div>
	);
}
