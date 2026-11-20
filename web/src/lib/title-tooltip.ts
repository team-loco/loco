import "./title-tooltip.css";

const ID = "loco-tip";
const DELAY = 350;
const WARM = 400;
const GAP = 8;
const TRUNCATION_DEPTH = 4;
const TRIGGER = "[data-slot='tooltip-trigger']";

interface Target {
	element: Element;
	text: string;
	/** Inside a Base UI tooltip trigger, which shows its own tooltip. */
	quiet: boolean;
}

function cutOff(element: Element): boolean {
	return getComputedStyle(element).textOverflow === "ellipsis" && element.scrollWidth > element.clientWidth;
}

/** Shows every `title`, and the full text of ellipsized text, in the app's tooltip instead of the browser's. */
export function showTitlesAsTooltips() {
	const tip = document.createElement("div");
	tip.id = ID;
	tip.className = "loco-tip";
	tip.setAttribute("role", "tooltip");
	document.body.append(tip);

	let over: Element | null = null;
	let anchor: Target | null = null;
	let title: string | null = null;
	let blanked = false;
	let labelled = false;
	let describedBy: string | null = null;
	let timer = 0;
	let warmUntil = 0;
	const restored = new WeakMap<Element, string>();

	function find(start: Element | null): Target | null {
		if (start === null) return null;
		const quiet = start.closest(TRIGGER) !== null;
		for (let element: Element | null = start, depth = 0; element !== null; element = element.parentElement, depth++) {
			if (element === anchor?.element) return anchor;
			const value = element.getAttribute("title");
			if (value !== null) return value === "" ? null : { element, text: value, quiet };
			if (!quiet && depth < TRUNCATION_DEPTH && cutOff(element))
				return { element, text: element.textContent.trim(), quiet };
		}
		return null;
	}

	function claim(target: Target) {
		release();
		anchor = target;
		title = target.element.getAttribute("title");
	}

	// the browser shows its own tooltip only on hover, so only a hovered anchor's title is blanked
	function blank() {
		if (anchor === null || title === null || blanked) return;
		const { element, text } = anchor;
		blanked = true;
		element.setAttribute("title", "");
		labelled = !element.hasAttribute("aria-label") && element.textContent.trim() === "";
		if (labelled) element.setAttribute("aria-label", text);
	}

	function release() {
		hide();
		if (anchor === null) return;
		const { element } = anchor;
		anchor = null;
		if (labelled) element.removeAttribute("aria-label");
		if (blanked && title !== null) {
			restored.set(element, title);
			element.setAttribute("title", title);
		}
		blanked = false;
		labelled = false;
		title = null;
	}

	function hover(target: Element | null) {
		const found = find(target);
		if (found !== null && found === anchor) {
			blank();
			return;
		}
		release();
		if (found === null) return;
		claim(found);
		blank();
		if (!found.quiet) timer = window.setTimeout(show, Date.now() < warmUntil ? 0 : DELAY);
	}

	function hide() {
		clearTimeout(timer);
		if (anchor === null || !tip.classList.contains("on")) return;
		if (describedBy === null) anchor.element.removeAttribute("aria-describedby");
		else anchor.element.setAttribute("aria-describedby", describedBy);
		warmUntil = Date.now() + WARM;
		tip.classList.remove("on");
	}

	function show() {
		if (!anchor?.element.isConnected) return;
		const { element, text } = anchor;
		const visible = element instanceof HTMLElement ? element.innerText.trim() : "";
		if (visible === text.trim() && element.scrollWidth <= element.clientWidth + 1) return;
		tip.textContent = text;
		tip.style.left = "0px";
		const box = element.getBoundingClientRect();
		const top = box.top - tip.offsetHeight - GAP;
		const left = box.left + box.width / 2 - tip.offsetWidth / 2;
		tip.style.top = `${top < GAP ? box.bottom + GAP : top}px`;
		tip.style.left = `${Math.max(GAP, Math.min(innerWidth - tip.offsetWidth - GAP, left))}px`;
		if (tip.classList.contains("on")) return;
		describedBy = element.getAttribute("aria-describedby");
		element.setAttribute("aria-describedby", describedBy === null ? ID : `${describedBy} ${ID}`);
		tip.classList.add("on");
	}

	function retitle(element: Element) {
		if (element !== anchor?.element) {
			const mine = restored.get(element) === element.getAttribute("title");
			restored.delete(element);
			if (!mine && over !== null && element.contains(over)) hover(over);
			return;
		}
		const value = element.getAttribute("title");
		if (value === "") return;
		title = value;
		if (value === null) {
			blanked = false;
			hide();
			return;
		}
		anchor.text = value;
		if (blanked) element.setAttribute("title", "");
		if (labelled) element.setAttribute("aria-label", value);
		if (!anchor.quiet) show();
	}

	new MutationObserver((records) => {
		for (const element of new Set(records.map((record) => record.target as Element))) retitle(element);
	}).observe(document.documentElement, { subtree: true, attributeFilter: ["title"] });

	document.addEventListener(
		"pointerover",
		(event) => {
			over = event.target instanceof Element ? event.target : null;
			hover(over);
		},
		true,
	);
	document.addEventListener(
		"pointerout",
		(event) => {
			over = event.relatedTarget instanceof Element ? event.relatedTarget : null;
			if (anchor !== null && !(over !== null && anchor.element.contains(over))) release();
		},
		true,
	);
	document.addEventListener(
		"focusin",
		(event) => {
			const target = event.target instanceof Element ? event.target : null;
			if (!target?.matches(":focus-visible")) return;
			const found = find(target);
			if (found === null) return;
			if (found !== anchor) claim(found);
			if (!found.quiet) show();
		},
		true,
	);
	document.addEventListener(
		"focusout",
		(event) => {
			if (anchor !== null && event.target instanceof Node && anchor.element.contains(event.target)) release();
		},
		true,
	);
	document.addEventListener("pointerdown", hide, true);
	document.addEventListener(
		"keydown",
		(event) => {
			if (event.key === "Escape") hide();
		},
		true,
	);
	window.addEventListener("scroll", hide, true);
	window.addEventListener("blur", hide);
}
