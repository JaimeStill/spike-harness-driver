/**
 * The pi driver's bridge: the one extension a driven Pi loads, with discovery off. It gives the
 * driver what Pi's RPC commands don't: tools the driver runs, and a structured response per
 * exchange. Both reach the driver as dialog requests on the RPC stream, which it answers.
 *
 * - Each tool in the spec file PI_DRIVER_TOOLS names is registered at load. A call asks the
 *   driver with a "pi-driver:call" input dialog, whose placeholder carries the call, and the
 *   driver's answer is the tool's result or error.
 * - As each prompt arrives, a "pi-driver:exchange" input dialog asks the driver for the
 *   exchange's schema. With one, the bridge registers the "respond" tool with that schema,
 *   which ends the run once the model calls it, and appends an instruction to call it to the
 *   prompt; without one, "respond" is inactive. The instruction goes in the prompt because Pi
 *   tells the model about a tool added mid-session only by name and schema, and a model with
 *   other tools to choose from then answers in text.
 *
 * Answers are JSON: {"result": "..."} or {"error": "..."} for a call, and {"schema": {...}} or
 * {} for an exchange.
 */

import { readFileSync } from "node:fs";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

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

// ask puts body to the driver as an input dialog titled title, and parses its answer. A signal
// that aborts, as when the run is cancelled, resolves the dialog without an answer.
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
		// Only the driver's prompts start an exchange; an extension's own messages don't.
		if (event.source === "extension") {
			return { action: "continue" };
		}
		const answer = await ask(ctx, EXCHANGE, {});
		const active = pi.getActiveTools().filter((name) => name !== RESPOND);
		if (answer.schema === undefined) {
			pi.setActiveTools(active);
			return { action: "continue" };
		}
		// Registering again replaces the previous exchange's schema.
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
}
