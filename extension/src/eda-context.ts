/**
 * Helpers that read EasyEDA project/document context via the official `eda`
 * object. Used both for the `context` frame sent on connect and for the
 * `context` block attached to each action response.
 */

import type { ContextFrame, ResponseContext } from './protocol';

/**
 * Map a numeric EDMT_EditorDocumentType to a stable string label used in the
 * protocol. Only schematic is meaningful for Phase 1; others are labelled for
 * completeness.
 *
 * @param documentType - numeric document type from getCurrentDocumentInfo
 * @returns a lowercase string label
 */
export function documentTypeLabel(documentType: number | undefined): string | undefined {
	if (documentType === undefined) {
		return undefined;
	}
	switch (documentType) {
		case EDMT_EditorDocumentType.HOME:
			return 'home';
		case EDMT_EditorDocumentType.BLANK:
			return 'blank';
		case EDMT_EditorDocumentType.SCHEMATIC_PAGE:
			return 'schematic';
		case EDMT_EditorDocumentType.PCB:
			return 'pcb';
		case EDMT_EditorDocumentType.SYMBOL_COMPONENT:
			return 'symbol';
		case EDMT_EditorDocumentType.FOOTPRINT:
			return 'footprint';
		case EDMT_EditorDocumentType.PANEL:
			return 'panel';
		default:
			return `type_${documentType}`;
	}
}

/**
 * Read the best-effort current context (project + active document). Every field
 * is optional; failures are swallowed so context never blocks an action.
 *
 * @returns a partial response context
 */
/**
 * Host page visibility (document.visibilityState) read through the real global,
 * since the extension sandbox shadows module-scope `document`. A hidden tab has
 * its timers throttled and its canvas left unpainted, so waits time out and
 * canvas captures go stale; callers report it rather than guess.
 */
export function pageVisibility(): string | undefined {
	try {
		const state = (globalThis as unknown as { window?: { document?: { visibilityState?: unknown } } }).window?.document?.visibilityState;
		return typeof state === 'string' ? state : undefined;
	}
	catch { return undefined; }
}

/**
 * Host page user agent, read through the real global like pageVisibility().
 * Reported raw at registration so the daemon can tell a desktop shell window
 * from a browser tab; undefined when the sandbox hides it.
 */
export function hostUserAgent(): string | undefined {
	try {
		const scope = globalThis as unknown as { window?: { navigator?: { userAgent?: unknown } }; navigator?: { userAgent?: unknown } };
		const ua = scope.window?.navigator?.userAgent ?? scope.navigator?.userAgent;
		return typeof ua === 'string' && ua ? ua : undefined;
	}
	catch { return undefined; }
}

export async function readResponseContext(): Promise<ResponseContext> {
	const context: ResponseContext = {};
	const visibility = pageVisibility();
	if (visibility) context.pageVisibility = visibility;

	try {
		const project = await eda.dmt_Project.getCurrentProjectInfo();
		if (project) {
			context.projectUuid = project.uuid;
			context.projectName = project.friendlyName || project.name;
		}
	}
	catch { /* best-effort */ }

	try {
		const doc = await eda.dmt_SelectControl.getCurrentDocumentInfo();
		if (doc) {
			context.documentUuid = doc.uuid;
			context.tabId = doc.tabId;
			const label = documentTypeLabel(doc.documentType);
			if (label) {
				context.documentType = label;
			}
		}
	}
	catch { /* best-effort */ }

	return context;
}

/**
 * Compare the two handler-boundary samples. This catches observed drift and
 * missing identity, but cannot detect an ABA switch between the samples.
 * The optional tab identity must be absent on both sides or equal on both.
 */
export function mutationContextError(before: ResponseContext, after: ResponseContext): string | undefined {
	for (const field of ['projectUuid', 'documentUuid', 'documentType'] as const) {
		if (typeof before[field] !== 'string' || !before[field]!.trim()
			|| typeof after[field] !== 'string' || !after[field]!.trim()) {
			return `handler-boundary ${field} unavailable`;
		}
		if (before[field] !== after[field]) return `handler-boundary ${field} changed`;
	}
	const beforeTab = before.tabId, afterTab = after.tabId;
	const absent = (value: unknown): boolean => value === undefined || value === null || value === '';
	if (absent(beforeTab) && absent(afterTab)) return undefined;
	if (typeof beforeTab !== 'string' || !beforeTab.trim()
		|| typeof afterTab !== 'string' || !afterTab.trim()) return 'handler-boundary tabId unavailable on one side';
	if (beforeTab !== afterTab) return 'handler-boundary tabId changed';
	return undefined;
}

/**
 * Read the EasyEDA client version for the `register` frame. Best-effort; falls
 * back to an empty string.
 *
 * @returns the EasyEDA version string, or '' if unavailable
 */
export function readEasyEdaVersion(): string {
	try {
		const version = eda.sys_Environment.getEditorCurrentVersion();
		if (typeof version === 'string') {
			return version;
		}
	}
	catch { /* best-effort */ }
	return '';
}

/**
 * Build the `context` frame sent to the daemon after a successful register.
 * Empty fields are omitted.
 *
 * @param windowId - the registered window id
 * @returns a context frame
 */
export async function buildContextFrame(windowId: string): Promise<ContextFrame> {
	const ctx = await readResponseContext();
	const frame: ContextFrame = {
		type: 'context',
		windowId,
	};
	if (ctx.projectUuid) {
		frame.projectUuid = ctx.projectUuid;
	}
	if (ctx.projectName) {
		frame.projectName = ctx.projectName;
	}
	if (ctx.documentUuid) {
		frame.documentUuid = ctx.documentUuid;
	}
	if (ctx.documentType) {
		frame.documentType = ctx.documentType;
	}
	if (ctx.tabId) {
		frame.tabId = ctx.tabId;
	}
	if (ctx.pageVisibility) {
		frame.pageVisibility = ctx.pageVisibility;
	}
	return frame;
}
