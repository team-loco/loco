import { Minus, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useState, useEffect } from "react";

interface NumberInputProps {
	label?: string;
	value: number;
	onChange: (value: number) => void;
	min?: number;
	max?: number;
	disabled?: boolean;
	className?: string;
}

export function NumberInput({
	label,
	value,
	onChange,
	min = 1,
	max,
	disabled = false,
	className = "",
}: NumberInputProps) {
	const [inputValue, setInputValue] = useState(String(value));

	useEffect(() => {
		setInputValue(String(value));
	}, [value]);

	const handleDecrement = () => {
		const newValue = Math.max(min, value - 1);
		onChange(newValue);
	};

	const handleIncrement = () => {
		const newValue = max ? Math.min(max, value + 1) : value + 1;
		onChange(newValue);
	};

	const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
		setInputValue(e.target.value);
	};

	const handleBlur = () => {
		const val = inputValue.trim();
		if (val === "") {
			setInputValue(String(value));
			return;
		}
		const num = parseInt(val, 10);
		if (!isNaN(num)) {
			const clampedValue = Math.max(min, max ? Math.min(max, num) : num);
			onChange(clampedValue);
		} else {
			setInputValue(String(value));
		}
	};

	return (
		<div className={`space-y-2 ${className}`}>
			{label && <Label className="text-sm font-medium">{label}</Label>}
			<div className="flex gap-2">
				<Button
					onClick={handleDecrement}
					disabled={disabled || value <= min}
					size="icon"
					type="button"
					variant="outline"
					className="h-9 w-12"
				>
					<Minus className="h-6 w-6" />
				</Button>
				<Input
					className="bg-background text-center font-semibold"
					disabled={disabled}
					onChange={handleInputChange}
					onBlur={handleBlur}
					inputMode="numeric"
					type="text"
					value={inputValue}
				/>
				<Button
					onClick={handleIncrement}
					disabled={disabled || (max ? value >= max : false)}
					size="icon"
					type="button"
					variant="outline"
					className="h-9 w-12"
				>
					<Plus className="h-6 w-6" />
				</Button>
			</div>
		</div>
	);
}
