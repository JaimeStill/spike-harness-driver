/**
 * forceRespond makes a provider request that offers the "respond" tool require a tool call, so
 * the model can't end the exchange in text. The bridge applies it in Pi's
 * before_provider_request hook, where the payload is the provider's own request body.
 *
 * Only OpenAI-shaped bodies change: Chat Completions, whose tools are {type: "function",
 * function: {name}}, and Responses, whose tools are {type: "function", name}. Both take
 * tool_choice "required": the model still chooses which tool, so it can call the exchange's
 * other tools before respond, and the server constrains its output to a tool call (llama.cpp
 * with a grammar). Any other body, such as Anthropic Messages, is returned unchanged.
 *
 * It returns undefined when the payload stays as it is, which Pi reads as "no replacement".
 */
export function forceRespond(payload: unknown, respond: string): unknown {
	if (typeof payload !== "object" || payload === null) {
		return undefined;
	}
	const tools = (payload as { tools?: unknown }).tools;
	if (!Array.isArray(tools) || !tools.some((tool) => offers(tool, respond))) {
		return undefined;
	}
	return { ...payload, tool_choice: "required" };
}

// offers reports whether tool is an OpenAI function tool named name, in either API's shape.
function offers(tool: unknown, name: string): boolean {
	if (typeof tool !== "object" || tool === null) {
		return false;
	}
	const t = tool as { type?: unknown; name?: unknown; function?: { name?: unknown } };
	return t.type === "function" && (t.function?.name === name || t.name === name);
}
