import { useEffect, useEffectEvent, type KeyboardEvent as ReactKeyboardEvent } from "react";

export function useListNavigation(keys: readonly string[], selected: string | null, onSelect: (key: string) => void) {
	const step = (event: KeyboardEvent) => {
		if (event.defaultPrevented || selected === null || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey)
			return;
		if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
		const target = event.target;
		if (
			target instanceof Element &&
			target.closest(
				'input, textarea, [contenteditable="true"], [role="menu"], [role="combobox"], [role="slider"], [role="spinbutton"], [role="tablist"], [role="radiogroup"], [role="listbox"], [role="tree"], [role="treegrid"], [role="grid"], [role="menubar"], [role="toolbar"], [data-slot="toggle-group"], [data-segmented], [aria-label="Span list"]',
			)
		)
			return;
		const index = keys.indexOf(selected);
		if (index < 0) return;
		const key = keys[index + (event.key === "ArrowDown" ? 1 : -1)];
		if (key === undefined) return;
		event.preventDefault();
		onSelect(key);
	};
	const onWindowKeyDown = useEffectEvent(step);
	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			onWindowKeyDown(event);
		};
		window.addEventListener("keydown", onKeyDown);
		return () => {
			window.removeEventListener("keydown", onKeyDown);
		};
	}, []);
	// Dialog popups contain arrow-key propagation.
	return (event: ReactKeyboardEvent) => {
		step(event.nativeEvent);
	};
}
