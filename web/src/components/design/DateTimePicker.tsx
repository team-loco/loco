import { useState } from "react";
import { CalendarIcon } from "lucide-react";
import { DayPicker } from "react-day-picker";
import "react-day-picker/style.css";

import { Button } from "@/components/design/Button";
import { Input } from "@/components/design/Input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/design/Popover";

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
					<DayPicker
						mode="single"
						selected={selected}
						{...(selected === undefined ? {} : { defaultMonth: selected })}
						autoFocus
						required
						onSelect={(date) => {
							const nextDay = `${String(date.getFullYear())}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
							onChange(`${nextDay}T${time || "00:00"}`);
							setOpen(false);
						}}
						className="m-0 text-[13px] [--rdp-accent-background-color:var(--panel2)] [--rdp-accent-color:var(--primary)] [--rdp-day-height:32px] [--rdp-day-width:32px] [--rdp-day_button-height:30px] [--rdp-day_button-width:30px]"
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
