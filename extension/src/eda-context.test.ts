/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { buildContextFrame, hostUserAgent, pageVisibility } from './eda-context';

function stubEda(): void {
	(globalThis as any).eda = {
		dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'p1', friendlyName: 'ceshi' }) },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: 'd1', tabId: 't1', documentType: 1 }) },
	};
}

test('context frame carries the host page visibility so a hidden tab is observable', async () => {
	stubEda();
	(globalThis as any).window = { document: { visibilityState: 'hidden' } };
	try {
		assert.equal(pageVisibility(), 'hidden');
		const frame = await buildContextFrame('w1');
		assert.equal(frame.pageVisibility, 'hidden');
		assert.equal(frame.documentUuid, 'd1');
	}
	finally { delete (globalThis as any).eda; delete (globalThis as any).window; }
});

test('unreadable visibility is omitted, never invented', async () => {
	stubEda();
	try {
		assert.equal(pageVisibility(), undefined);
		const frame = await buildContextFrame('w1');
		assert.equal('pageVisibility' in frame, false);
	}
	finally { delete (globalThis as any).eda; }
});

test('host user agent is read raw from the real window, never classified here', () => {
	const ua = 'Mozilla/5.0 (Macintosh) AppleWebKit/537.36 edadesk/0.0.1 Chrome/152.0 Electron/44.4.5 Safari/537.36';
	(globalThis as any).window = { navigator: { userAgent: ua } };
	try { assert.equal(hostUserAgent(), ua); }
	finally { delete (globalThis as any).window; }
	(globalThis as any).window = { navigator: { userAgent: '' } };
	try { assert.notEqual(hostUserAgent(), ''); }
	finally { delete (globalThis as any).window; }
});
