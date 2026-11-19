const DEPTH = 4;
const generated = new WeakSet<HTMLElement>();

function cutOff(element: HTMLElement): boolean {
	return getComputedStyle(element).textOverflow === "ellipsis" && element.scrollWidth > element.clientWidth;
}

// Gives ellipsized text its full value as a title on hover and keyboard focus.
function addTitle(event: Event) {
	let element = event.target instanceof HTMLElement ? event.target : null;
	for (let depth = 0; element !== null && depth < DEPTH; depth++, element = element.parentElement) {
		if (element.title !== "" && !generated.has(element)) return;
		if (cutOff(element)) {
			element.title = element.textContent.trim();
			generated.add(element);
			return;
		}
	}
}

export function showFullTextOnTruncation() {
	document.addEventListener("pointerover", addTitle, { passive: true });
	document.addEventListener("focusin", addTitle, { passive: true });
}
