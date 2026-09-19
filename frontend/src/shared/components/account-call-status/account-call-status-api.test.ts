import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { uniqueAccountIds } from "./account-call-status-ids.ts";

describe("uniqueAccountIds", () => {
  it("drops blanks and duplicates while keeping order of first seen", () => {
    assert.deepEqual(uniqueAccountIds([undefined, "12", "8", "12", "", "  ", "8"]), ["12", "8"]);
  });

  it("trims surrounding whitespace before deduping", () => {
    assert.deepEqual(uniqueAccountIds([" 12 ", "12", "\t8"]), ["12", "8"]);
  });
});
