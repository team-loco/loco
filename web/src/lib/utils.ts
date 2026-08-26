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

/**
 * Looks up a value keyed by an open protobuf enum. The map is exhaustive over the
 * enum this build knows about, but a newer server can send a value outside it —
 * so the lookup is deliberately treated as partial at runtime.
 */
export function lookupEnum<K extends number, V>(
  map: Record<K, V>,
  key: K,
  fallback: V,
): V {
  return (map as Partial<Record<K, V>>)[key] ?? fallback;
}
