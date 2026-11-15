import { formatHourMinute, parseClockInput } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { useState } from "react";
import { CalendarIcon } from "lucide-react";

import { Button } from "@/components/design/Button";
import { Calendar } from "@/components/design/Calendar";
import { Input } from "@/components/design/Input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/design/Popover";
import { joinLocalInput, toLocalInput, fromLocalInput } from "@/lib/obs-time";

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
	const pref = useTimePreferences();
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
							const [nextDay = ""] = toLocalInput(date.getTime(), { zone: "local", clock: "24" }).split("T", 1);
							onChange(joinLocalInput(nextDay, time || "00:00"));
							setOpen(false);
						}}
					/>
				</PopoverContent>
			</Popover>
			<ClockInput key={`${pref.zone}:${pref.clock}`} label={label} day={day} time={time} onChange={onChange} />
		</div>
	);
}

function ClockInput({
	label,
	day,
	time,
	onChange,
}: {
	label: string;
	day: string;
	time: string;
	onChange: (value: string) => void;
}) {
	const pref = useTimePreferences();
	const [draft, setDraft] = useState<string | null>(null);
	const ms = fromLocalInput(joinLocalInput(day, time), pref);
	const shown = Number.isFinite(ms) ? formatHourMinute(ms, pref) : time;
	const invalid = draft !== null && parseClockInput(draft, pref) === null;
	return (
		<Input
			type="text"
			aria-label={`${label} time`}
			aria-invalid={invalid}
			placeholder={pref.clock === "12" ? "1:00 PM" : "13:00"}
			value={draft ?? shown}
			onChange={(event) => {
				const value = event.target.value;
				setDraft(value);
				onChange(joinLocalInput(day, parseClockInput(value, pref) ?? ""));
			}}
			onBlur={() => {
				if (!invalid) setDraft(null);
			}}
			className="h-8 w-[116px] shrink-0 font-mono"
		/>
	);
}
