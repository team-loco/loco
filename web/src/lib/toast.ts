// eslint-disable-next-line no-restricted-imports -- the one wrapper around sonner's toast
import { toast as sonner, type ExternalToast } from "sonner";

// eslint-disable-next-line no-restricted-imports -- toasts are raised from event handlers
import { readStorage } from "@/lib/storage";

export const toastsKey = "loco:toasts:v1";

type Show = (message: Parameters<typeof sonner.success>[0], data?: ExternalToast) => string | number;

// With toasts off, only a toast that asks for an answer or stays until dismissed still shows. Errors and warnings always show.
function quiet(show: Show): Show {
	return (message, data) =>
		readStorage(toastsKey) !== "off" || data?.action !== undefined || data?.duration === Infinity
			? show(message, data)
			: "";
}

export const toast = Object.assign(quiet(sonner), sonner, { success: quiet(sonner.success), info: quiet(sonner.info) });
