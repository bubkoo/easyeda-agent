/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { getSchematicNetlistFile, NETLIST_EXPORT_QUIET_MS } from './schematic-netlist-file';

for (const mode of ['success', 'empty', 'export-error', 'drift', 'busy-other', 'unsupported', 'readback-error']) {
	test(`netlist transaction boundary: ${mode}`, async t => {
		const globals = globalThis as any, old = { eda: globals.eda, window: globals.window };
		t.after(() => Object.assign(globals, old));
		let tab = { uuid: 'page', tabId: 'page@project' };
		const calls: Array<string> = [];
		const file = { name: 'netlist.json' };
		const document = { uuid: 'page', tabId: 'page@project', profileSetting: { readonlyMode: false } };
	const runner = { running: false, currentAction: null as any };
		globals.window = mode === 'unsupported' ? {} : { SCH: { docMemoryManager: { getActiveDoc: () => document }, app: { actionRunner: runner } } };
		globals.eda = {
			dmt_SelectControl: { getCurrentDocumentInfo: async () => {
				if (mode === 'readback-error' && calls.length) throw new Error('host read failed');
				return { ...tab };
			} },
			dmt_EditorControl: { activateDocument: async () => { throw new Error('must not refocus'); } },
			sch_ManufactureData: { getNetlistFile: async () => {
				calls.push('export');
				if (mode === 'drift') tab = { uuid: 'other', tabId: 'other@project' };
				if (mode === 'busy-other') Object.assign(runner, { running: true, currentAction: { name: 'copy', status: 'running' } });
				if (mode === 'export-error') throw new Error('export failed');
				return mode === 'empty' ? undefined : file;
			} },
		};
		if (mode === 'success' || mode === 'empty') assert.equal(await getSchematicNetlistFile(), mode === 'empty' ? undefined : file);
		else await assert.rejects(getSchematicNetlistFile(), /export failed|Cannot verify schematic readiness/);
		assert.deepEqual(calls, mode === 'unsupported' ? [] : ['export']);
	});
}

test('netlist export waits for the RealTimeSync the host queues after export resolves (#256/#257)', async t => {
	const globals = globalThis as any, old = { eda: globals.eda, window: globals.window };
	const timers: Array<ReturnType<typeof setTimeout>> = [];
	t.after(() => { timers.forEach(clearTimeout); Object.assign(globals, old); });
	const tab = { uuid: 'page', tabId: 'page@project' };
	const document = { ...tab, profileSetting: { readonlyMode: false } };
	const runner = { running: false, currentAction: null as any };
	let syncEndedAt = 0;
	globals.window = { SCH: { docMemoryManager: { getActiveDoc: () => document }, app: { actionRunner: runner } } };
	globals.eda = {
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ ...tab }) },
		sch_ManufactureData: { getNetlistFile: async () => {
			// Live 4.1.78: the sync starts 7–21 ms after export resolves and runs 35–70 ms.
			timers.push(setTimeout(() => Object.assign(runner, { running: true, currentAction: { name: 'RealTimeSync', status: 'running' } }), 15));
			timers.push(setTimeout(() => { Object.assign(runner, { running: false, currentAction: null }); syncEndedAt = Date.now(); }, 75));
			return { name: 'netlist.json' };
		} },
	};
	await getSchematicNetlistFile();
	assert.ok(syncEndedAt > 0, 'returned while the post-export sync had not yet run');
	assert.ok(Date.now() - syncEndedAt >= NETLIST_EXPORT_QUIET_MS - 5);
});
