import { apiRequest } from "@/shared/api/client";
import { createObjectDecoder, hasShape, isArrayOf, isBoolean, isNumber, isOneOf, isOptional, isRecordOf, isString } from "@/shared/api/decoder";
import { uniqueAccountIds } from "@/shared/components/account-call-status/account-call-status-ids";

export { uniqueAccountIds } from "@/shared/components/account-call-status/account-call-status-ids";

export type CallLevelName = "failed" | "degraded" | "soft" | "healthy";
export type CallStatusWindow = "1h" | "6h" | "24h" | "7d";

export type CallStatusThresholdsDTO = {
  softTPS: number;
  hardTPS: number;
  minGenMs: number;
  failClosed: boolean;
};

export type CallStatusCountsDTO = {
  healthy: number;
  soft: number;
  degraded: number;
  failed: number;
};

export type CallStatusSampleDTO = {
  id: string;
  t: string;
  l: number;
};

export type AccountCallStatusDTO = {
  accountId: string;
  sampleCount: number;
  counts: CallStatusCountsDTO;
  last?: string;
  samples: CallStatusSampleDTO[];
};

export type CallStatusBatchDTO = {
  generatedAt: string;
  thresholds: CallStatusThresholdsDTO;
  accounts: Record<string, AccountCallStatusDTO>;
};

export type CallStatusPointDTO = {
  id: string;
  requestId: string;
  t: string;
  level: CallLevelName;
  statusCode: number;
  errorCode?: string;
  class?: string;
  tps: number;
  firstTokenMs?: number;
  durationMs: number;
  outputTokens: number;
  model?: string;
  accountName?: string;
  nodeName?: string;
};

export type CallStatusDetailDTO = {
  accountId: string;
  accountName?: string;
  window: CallStatusWindow;
  range: { start: string; end: string };
  thresholds: CallStatusThresholdsDTO;
  totals: CallStatusCountsDTO & { calls: number; maxTPS: number };
  points: CallStatusPointDTO[];
  truncated: boolean;
};

export type RequestCallStatusDTO = {
  period: "24h" | "7d" | "30d" | "90d";
  range: { start: string; end: string };
  thresholds: CallStatusThresholdsDTO;
  totals: CallStatusCountsDTO & { calls: number; maxTPS: number };
  points: CallStatusPointDTO[];
  truncated: boolean;
};

export type CallStatusThresholdInput = {
  softTPS?: number;
  hardTPS?: number;
  minGenMs?: number;
  failClosed?: boolean;
};

const thresholdsValidator = hasShape({
  softTPS: isNumber, hardTPS: isNumber, minGenMs: isNumber, failClosed: isBoolean,
});
const countsValidator = hasShape({
  healthy: isNumber, soft: isNumber, degraded: isNumber, failed: isNumber,
});
const sampleValidator = hasShape({ id: isString, t: isString, l: isNumber });
const accountValidator = hasShape({
  accountId: isString, sampleCount: isNumber, counts: countsValidator,
  last: isOptional(isString), samples: isArrayOf(sampleValidator),
});
const decodeBatch = createObjectDecoder<CallStatusBatchDTO>("account call status", {
  generatedAt: isString, thresholds: thresholdsValidator, accounts: isRecordOf(accountValidator),
});
const pointValidator = hasShape({
  id: isString, requestId: isString, t: isString, level: isOneOf("failed", "degraded", "soft", "healthy"),
  statusCode: isNumber, errorCode: isOptional(isString), class: isOptional(isString), tps: isNumber,
  firstTokenMs: isOptional(isNumber), durationMs: isNumber, outputTokens: isNumber,
  model: isOptional(isString), accountName: isOptional(isString), nodeName: isOptional(isString),
});
const totalsValidator = hasShape({
  calls: isNumber, healthy: isNumber, soft: isNumber, degraded: isNumber, failed: isNumber, maxTPS: isNumber,
});
const decodeDetail = createObjectDecoder<CallStatusDetailDTO>("account call status detail", {
  accountId: isString, accountName: isOptional(isString), window: isOneOf("1h", "6h", "24h", "7d"),
  range: hasShape({ start: isString, end: isString }), thresholds: thresholdsValidator,
  totals: totalsValidator, points: isArrayOf(pointValidator), truncated: isBoolean,
});
const decodeRequestCallStatus = createObjectDecoder<RequestCallStatusDTO>("request call status", {
  period: isOneOf("24h", "7d", "30d", "90d"),
  range: hasShape({ start: isString, end: isString }), thresholds: thresholdsValidator,
  totals: totalsValidator, points: isArrayOf(pointValidator), truncated: isBoolean,
});

function appendThresholds(query: URLSearchParams, thresholds?: CallStatusThresholdInput): void {
  if (thresholds?.softTPS) query.set("softTPS", String(thresholds.softTPS));
  if (thresholds?.hardTPS) query.set("hardTPS", String(thresholds.hardTPS));
  if (thresholds?.minGenMs) query.set("minGenMs", String(thresholds.minGenMs));
  if (thresholds?.failClosed !== undefined) query.set("failClosed", String(thresholds.failClosed));
}

const ACCOUNT_CALL_STATUS_MAX_IDS = 100;

export async function getAccountCallStatus(input: {
  accountIds: string[];
  limit?: number;
  thresholds?: CallStatusThresholdInput;
}, signal?: AbortSignal): Promise<CallStatusBatchDTO> {
  const ids = uniqueAccountIds(input.accountIds);
  if (ids.length <= ACCOUNT_CALL_STATUS_MAX_IDS) {
    return fetchAccountCallStatus({ ...input, accountIds: ids }, signal);
  }
  const chunks: string[][] = [];
  for (let index = 0; index < ids.length; index += ACCOUNT_CALL_STATUS_MAX_IDS) {
    chunks.push(ids.slice(index, index + ACCOUNT_CALL_STATUS_MAX_IDS));
  }
  const batches = await Promise.all(chunks.map((accountIds) => fetchAccountCallStatus({ ...input, accountIds }, signal)));
  const accounts: CallStatusBatchDTO["accounts"] = {};
  for (const batch of batches) {
    Object.assign(accounts, batch.accounts);
  }
  return {
    generatedAt: batches[0]?.generatedAt ?? "",
    thresholds: batches[0]?.thresholds ?? { softTPS: 0, hardTPS: 0, minGenMs: 0, failClosed: false },
    accounts,
  };
}

function fetchAccountCallStatus(input: {
  accountIds: string[];
  limit?: number;
  thresholds?: CallStatusThresholdInput;
}, signal?: AbortSignal): Promise<CallStatusBatchDTO> {
  const query = new URLSearchParams();
  if (input.accountIds.length) query.set("accountIds", input.accountIds.join(","));
  if (input.limit) query.set("limit", String(input.limit));
  appendThresholds(query, input.thresholds);
  return apiRequest(`/api/admin/v1/request-audits/account-call-status?${query}`, { signal }, decodeBatch);
}

export function getAccountCallStatusDetail(input: {
  accountId: string;
  window: CallStatusWindow;
  includeProbes?: boolean;
  thresholds?: CallStatusThresholdInput;
}, signal?: AbortSignal): Promise<CallStatusDetailDTO> {
  const query = new URLSearchParams({ window: input.window });
  if (input.includeProbes) query.set("includeProbes", "true");
  appendThresholds(query, input.thresholds);
  return apiRequest(`/api/admin/v1/request-audits/accounts/${input.accountId}/call-status?${query}`, { signal }, decodeDetail);
}

export function getRequestCallStatus(input: {
  period: RequestCallStatusDTO["period"];
  search?: string;
  model?: string;
  status?: string;
  mode?: string;
  key?: string;
  account?: string;
  thresholds?: CallStatusThresholdInput;
}, signal?: AbortSignal): Promise<RequestCallStatusDTO> {
  const query = new URLSearchParams({ period: input.period });
  if (input.search) query.set("search", input.search);
  if (input.model) query.set("model", input.model);
  if (input.status) query.set("status", input.status);
  if (input.mode) query.set("mode", input.mode);
  if (input.key) query.set("key", input.key);
  if (input.account) query.set("account", input.account);
  appendThresholds(query, input.thresholds);
  return apiRequest(`/api/admin/v1/request-audits/call-status?${query}`, { signal }, decodeRequestCallStatus);
}

export const CALL_LEVEL_HEIGHT: Record<number, number> = { 0: 0.28, 1: 0.5, 2: 0.74, 3: 1 };
export const CALL_LEVEL_CLASS: Record<number, string> = {
  0: "fill-red-500",
  1: "fill-orange-500",
  2: "fill-amber-500",
  3: "fill-emerald-500",
};
