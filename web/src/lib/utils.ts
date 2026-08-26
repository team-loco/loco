import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function formatShortId(id: string): string {
  if (!id) return "";
  return id.slice(-8);
}

/** Returns `fallback` when `value` is null, undefined, or the empty string. */
export function nonEmpty(
  value: string | null | undefined,
  fallback: string,
): string {
  return value == null || value === "" ? fallback : value;
}
