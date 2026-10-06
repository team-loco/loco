export { cn } from "cn"

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
