import { formatCompactDateTime } from "@/shared/lib/format";

export function formatCallStatusTick(value: string, locale: string, style: "time" | "datetime"): string {
  const compact = formatCompactDateTime(value, locale);
  return style === "datetime" ? compact.slice(5, 16) : compact.slice(11, 16);
}
