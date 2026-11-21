/**
 * The pi driver's bridge: the one extension a driven Pi loads, with discovery off. It gives the
 * driver what Pi's RPC commands don't: tools the driver runs, and a structured response per
 * exchange. Both reach the driver as dialog requests on the RPC stream, which it answers.
 *
 * - Each tool in the spec file PI_DRIVER_TOOLS names is registered at load. A call asks the
 *   driver with a "pi-driver:call" input dialog, whose placeholder carries the call, and the
 *   driver's answer is the tool's result or error.
 * - As each of the driver's prompts arrives, a "pi-driver:exchange" input dialog asks the
 *   driver for the exchange's schema; an extension's own messages start no exchange. With a
 *   schema, the bridge registers the "respond" tool with it, replacing the previous exchange's
 *   schema, which ends the run once the model calls it, and appends to the prompt an
 *   instruction to call it; without one, "respond" is inactive. The instruction goes in the
 *   prompt because Pi tells the model about a tool added mid-session only by name and schema,
 *   and a model with other tools to choose from then answers in text. While "respond" is
 *   active, a before_provider_request hook sets the request's tool_choice to "required" (see
 *   respond.ts), so the model can't end the exchange in text either. The request's tools are
 *   the active ones, so respond is in them only during an exchange that has a schema.
 *
 * Answers are JSON: {"result": "..."} or {"error": "..."} for a call, and {"schema": {...}} or
 * {} for an exchange. A dialog whose signal aborts, as when the run is cancelled, resolves
 * without an answer, and the call fails.
 */

import { readFileSync } from "node:fs";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { forceRespond } from "./respond.ts";

const CALL = "pi-driver:call";
const EXCHANGE = "pi-driver:exchange";
const RESPOND = "respond";

interface ToolSpec {
	name: string;
	description: string;
	schema: unknown;
}

interface Answer {
	result?: string;
	error?: string;
	schema?: unknown;
}

async function ask(ctx: ExtensionContext, title: string, body: unknown, signal?: AbortSignal): Promise<Answer> {
	const answer = await ctx.ui.input(title, JSON.stringify(body), { signal });
	if (answer === undefined) {
		throw new Error(`${title}: the driver gave no answer`);
	}
	return JSON.parse(answer) as Answer;
}

export default function (pi: ExtensionAPI) {
	const spec = process.env.PI_DRIVER_TOOLS;
	const tools: ToolSpec[] = spec ? JSON.parse(readFileSync(spec, "utf8")) : [];
	for (const tool of tools) {
		pi.registerTool({
			name: tool.name,
			label: tool.name,
			description: tool.description,
			parameters: Type.Unsafe(tool.schema),
			async execute(callId, args, signal, _onUpdate, ctx) {
				const answer = await ask(ctx, CALL, { tool: tool.name, callId, args }, signal);
				if (answer.error !== undefined) {
					throw new Error(answer.error);
				}
				return { content: [{ type: "text", text: answer.result ?? "" }], details: undefined };
			},
		});
	}

	pi.on("input", async (event, ctx) => {
		if (event.source === "extension") {
			return { action: "continue" };
		}
		const answer = await ask(ctx, EXCHANGE, {});
		const active = pi.getActiveTools().filter((name) => name !== RESPOND);
		if (answer.schema === undefined) {
			pi.setActiveTools(active);
			return { action: "continue" };
		}
		pi.registerTool({
			name: RESPOND,
			label: RESPOND,
			description:
				"Give your final answer as this tool's arguments. Call it exactly once, as your last action, instead of answering in text.",
			promptSnippet: "Give the final answer as structured data",
			parameters: Type.Unsafe(answer.schema),
			async execute(_callId, args) {
				return { content: [{ type: "text", text: "Recorded." }], details: args, terminate: true };
			},
		});
		pi.setActiveTools([...active, RESPOND]);
		return {
			action: "transform",
			text: `${event.text}\n\nGive your final answer by calling the ${RESPOND} tool with the answer as its arguments, not in text.`,
		};
	});

	pi.on("before_provider_request", (event) => forceRespond(event.payload, RESPOND));
}
