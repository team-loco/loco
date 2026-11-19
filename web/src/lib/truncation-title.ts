const DEPTH = 4;
const generated = new WeakSet<HTMLElement>();

function cutOff(element: HTMLElement): boolean {
	return getComputedStyle(element).textOverflow === "ellipsis" && element.scrollWidth > element.clientWidth;
}

// Gives ellipsized text its full value as a title on hover and keyboard focus, and drops the title once the text fits.
function syncTitle(event: Event) {
	let element = event.target instanceof HTMLElement ? event.target : null;
	for (let depth = 0; element !== null && depth < DEPTH; depth++, element = element.parentElement) {
		if (element.title !== "" && !generated.has(element)) return;
		if (cutOff(element)) {
			element.title = element.textContent.trim();
			generated.add(element);
			return;
		}
		if (generated.has(element)) {
			element.removeAttribute("title");
			generated.delete(element);
		}
	}
}

export function showFullTextOnTruncation() {
	document.addEventListener("pointerover", syncTitle, { passive: true });
	document.addEventListener("focusin", syncTitle, { passive: true });
}
