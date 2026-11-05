import assert from "node:assert/strict"
import test from "node:test"
import { errorDetail } from "./diagnostics.ts"

test("generation errors retain useful identity without request payloads or credentials", () => {
  const detail = errorDetail({ type: "provider.unavailable", status: 503,
    message: 'Gateway failed sk-secret-key Bearer secret-token api_key=other-secret "apiKey":"quoted-secret" https://user:password@host/path?token=secret',
    request: { headers: { authorization: "SECRET_HEADER" }, body: "SECRET_PROMPT" }, response: { body: "SECRET_RESPONSE" } })
  assert.equal(detail.type, "provider.unavailable")
  assert.equal(detail.status, 503)
  assert.match(detail.message, /^Gateway failed/)
  assert.doesNotMatch(JSON.stringify(detail), /secret|SECRET_/)
  assert.deepEqual(errorDetail(new Error("Gateway timed out")), { type: "Error", message: "Gateway timed out" })
  assert.equal(errorDetail({}).message, "Title operation failed")
})
