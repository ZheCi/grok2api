import { apiRequest } from "@/shared/api/client";
import { createObjectDecoder, isArrayOf, isOneOf, isOptional, isString } from "@/shared/api/decoder";

export type NodeAvoidanceStatus = "healthy" | "disabled" | "avoiding";

export type NodeAvoidanceItem = {
  id: string;
  name: string;
  status: NodeAvoidanceStatus;
  avoidUntil?: string;
};

const decodeNodeAvoidance = createObjectDecoder<{ items: NodeAvoidanceItem[] }>("node avoidance", {
  items: isArrayOf((value) => {
    if (typeof value !== "object" || value === null) return false;
    const item = value as Record<string, unknown>;
    return isString(item.id) && isString(item.name) && isOneOf("healthy", "disabled", "avoiding")(item.status) && isOptional(isString)(item.avoidUntil);
  }),
});

export function getNodeAvoidance(signal?: AbortSignal): Promise<{ items: NodeAvoidanceItem[] }> {
  return apiRequest("/api/admin/v1/egress-nodes/avoidance", { signal }, decodeNodeAvoidance);
}
