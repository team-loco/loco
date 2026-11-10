import { useState } from "react";
import { CalendarIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { Calendar } from "@/components/design/Calendar";
import { Input } from "@/components/design/Input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/design/Popover";
import { toLocalInput } from "@/lib/obs-time";

export function DateTimePicker({
	label,
	value,
	onChange,
}: {
	label: string;
	value: string;
	onChange: (value: string) => void;
}) {
	const [open, setOpen] = useState(false);
	const [day = "", time = ""] = value.split("T");
	const parsed = new Date(`${day}T00:00`);
	const selected = Number.isNaN(parsed.getTime()) ? undefined : parsed;
	return (
		<div className="flex min-w-0 items-center gap-1.5">
			<Popover open={open} onOpenChange={setOpen}>
				<PopoverTrigger
					render={<Button variant="outline" size="sm" />}
					aria-label={`${label} date`}
					className="min-w-0 flex-1 justify-start font-mono"
				>
					<CalendarIcon />
					{day || "Choose date"}
				</PopoverTrigger>
				<PopoverContent align="start" className="w-auto max-w-[calc(100vw-24px)] p-2" aria-label={`${label} calendar`}>
					<Calendar
						mode="single"
						selected={selected}
						{...(selected === undefined ? {} : { defaultMonth: selected })}
						autoFocus
						required
						onSelect={(date) => {
							const [nextDay = ""] = toLocalInput(date.getTime()).split("T", 1);
							onChange(`${nextDay}T${time || "00:00"}`);
							setOpen(false);
						}}
					/>
				</PopoverContent>
			</Popover>
			<Input
				type="time"
				aria-label={`${label} time`}
				value={time}
				onChange={(event) => {
					onChange(`${day}T${event.target.value}`);
				}}
				className="h-8 w-[104px] shrink-0 font-mono"
			/>
		</div>
	);
}
