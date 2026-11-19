import { Field } from "./Field";
import { Textarea } from "./Textarea";

export interface CommandValue {
	command: string;
	args: string;
}

export function CommandFields({
	value,
	errors,
	disabled = false,
	describedBy,
	onChange,
}: {
	value: CommandValue;
	errors?: Partial<Record<keyof CommandValue, string | null>>;
	disabled?: boolean;
	describedBy?: string | undefined;
	onChange: (patch: Partial<CommandValue>) => void;
}) {
	const field = (key: keyof CommandValue, label: string, hint: string, placeholder: string) => (
		<Field label={label} hint={hint} error={errors?.[key]}>
			<Textarea
				rows={3}
				value={value[key]}
				placeholder={placeholder}
				className="font-mono"
				spellCheck={false}
				autoComplete="off"
				disabled={disabled}
				{...(describedBy === undefined ? {} : { "aria-describedby": describedBy })}
				onChange={(e) => {
					onChange({ [key]: e.target.value });
				}}
			/>
		</Field>
	);
	return (
		<div className="grid grid-cols-[repeat(auto-fit,minmax(min(220px,100%),1fr))] gap-x-[18px] gap-y-3.5">
			{field("command", "Command", "One entry per line. Replaces the image ENTRYPOINT; empty keeps it.", "/app/server")}
			{field(
				"args",
				"Arguments",
				"One entry per line. Replaces the image CMD; empty keeps it.",
				"--port=8080\n--log-level=info",
			)}
		</div>
	);
}
