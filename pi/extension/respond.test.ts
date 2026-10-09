// Tests of forceRespond, run by node's test runner in `mise run check`. The payloads are the
// shapes Pi's openai-completions and openai-responses providers send, cut to the fields
// forceRespond reads.

import assert from "node:assert/strict";
import { test } from "node:test";
import { forceRespond } from "./respond.ts";

const completionsTool = (name: string) => ({ type: "function", function: { name, parameters: {} } });
const responsesTool = (name: string) => ({ type: "function", name, parameters: {} });

test("a Chat Completions request that offers respond requires a tool call", () => {
	const payload = { model: "m", messages: [], tools: [completionsTool("lookup"), completionsTool("respond")] };
	assert.deepEqual(forceRespond(payload, "respond"), { ...payload, tool_choice: "required" });
});

test("a Responses request that offers respond requires a tool call", () => {
	const payload = { model: "m", input: [], tools: [responsesTool("respond")], tool_choice: "auto" };
	assert.deepEqual(forceRespond(payload, "respond"), { ...payload, tool_choice: "required" });
});

test("a request without respond is left as it is", () => {
	assert.equal(forceRespond({ model: "m", tools: [completionsTool("lookup")] }, "respond"), undefined);
	assert.equal(forceRespond({ model: "m", messages: [] }, "respond"), undefined);
});

test("an Anthropic Messages request is left as it is", () => {
	const payload = { model: "m", tools: [{ name: "respond", input_schema: {} }] };
	assert.equal(forceRespond(payload, "respond"), undefined);
});

test("the payload is not changed in place", () => {
	const payload = { model: "m", tools: [completionsTool("respond")] };
	forceRespond(payload, "respond");
	assert.equal("tool_choice" in payload, false);
});
