import { cn } from "@/lib/utils";

import { ToggleGroup, ToggleGroupItem } from "./ToggleGroup";

export function SegmentedChoice({
	label,
	options,
	value,
	onChange,
	disabled = false,
	clearable = false,
	describedBy,
	className,
}: {
	label: string;
	options: readonly { value: string; label: string }[];
	value: string;
	onChange: (value: string) => void;
	disabled?: boolean;
	clearable?: boolean;
	describedBy?: string | undefined;
	className?: string;
}) {
	return (
		// Narrow screens cannot fit a row of sizes, so the options wrap into an even grid there.
		<ToggleGroup
			variant="segmented"
			aria-label={label}
			aria-describedby={describedBy}
			className={cn("grid w-full grid-cols-4 sm:flex sm:w-max", className)}
			value={value === "" ? [] : [value]}
			disabled={disabled}
			onValueChange={(next: string[]) => {
				const picked = next[0];
				// Without clearable a control always has one choice, so clicking the chosen one keeps it.
				if (picked !== undefined) onChange(picked);
				else if (clearable) onChange("");
			}}
		>
			{options.map((o) => (
				<ToggleGroupItem key={o.value} value={o.value}>
					{o.label}
				</ToggleGroupItem>
			))}
		</ToggleGroup>
	);
}
