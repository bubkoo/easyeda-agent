import assert from 'node:assert/strict';
import { test } from 'node:test';
import { pcbExport3D, pcbExport3DShell, MAX_3D_BYTES } from './pcb-export-3d';

function host(options: { drift?: boolean; bindingDrift?: boolean; missing?: boolean; empty?: boolean; oversized?: boolean } = {}) {
	let calls = 0, reads = 0, modelReads = 0;
	(globalThis as any).EDMT_EditorDocumentType = { PCB: 3 };
	(globalThis as any).eda = {
		dmt_Project: { getCurrentProjectInfo: async () => ({ uuid:'p' }) },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: options.drift && ++reads > 1 ? 'other' : 'b', documentType:3, tabId:'t' }) },
		pcb_PrimitiveComponent: { getAll: async () => { const model = options.bindingDrift && ++modelReads > 1 ? 'changed' : 'm'; return [
			{ getState_PrimitiveId:()=> 'c1', getState_Designator:()=> 'U1', getState_Model3D:()=> ({ uuid:model, libraryUuid:'l' }) },
			{ getState_PrimitiveId:()=> 'c2', getState_Designator:()=> 'R1', getState_Model3D:()=> undefined },
		]; } },
		pcb_ManufactureData: { get3DFile: async (...args: unknown[]) => {
			calls++; assert.equal(args[1], 'obj'); assert.equal(args[4], false); assert.deepEqual(args[2], ['Component Model']);
			if (options.missing) return undefined;
			if (options.oversized) return { size: MAX_3D_BYTES+1 };
			return new File([options.empty ? '' : 'real-host-bytes'], 'board.zip');
		} },
	};
	return () => calls;
}

test('3D export preserves official bytes/container and reports missing bindings honestly', async () => {
	const calls = host();
	const res = await pcbExport3D({});
	assert.equal(calls(), 1);
	assert.equal(res.artifacts?.[0].fileName, 'board.zip');
	assert.equal(Buffer.from(res.artifacts![0].inlineBase64!, 'base64').toString(), 'real-host-bytes');
	assert.deepEqual((res.result?.models as any[]).map(m=>m.status), ['bound', 'unbound']);
	assert.equal(res.result?.modelCoverage, 'binding-only');
});

test('3D export refuses target drift, unavailable/empty/oversized files', async () => {
	for (const opts of [{ drift:true }, { bindingDrift:true }, { missing:true }, { empty:true }, { oversized:true }]) {
		host(opts); await assert.rejects(pcbExport3D({}));
	}
});

test('empty, unavailable and oversized exports have distinct actionable errors', async () => {
	for (const [opts, message] of [[{missing:true}, /returned no file/], [{empty:true}, /empty file/], [{oversized:true}, /20971521 bytes/]] as const) {
		host(opts); await assert.rejects(pcbExport3D({}), message);
	}
});

test('invalid export options never reach official exporter', async () => {
	const calls = host();
	for (const payload of [{ fileType:'glb' }, { fileName:'../bad' }, { autoGenerateModels:'true' }, { modelMode:'unknown' }, { surprise:true }, { elements:['fake'] }, { elements:['Via','Via'] }]) {
		await assert.rejects(pcbExport3D(payload));
	}
	assert.equal(calls(), 0);
});

test('3D shell export returns the official file and refuses a board without a shell', async () => {
	host();
	let shell: File | undefined = new File(['solid shell'], 'shell.stl');
	(globalThis as any).eda.pcb_ManufactureData.get3DShellFile = async (_n: string, type: string) => { assert.equal(type, 'stl'); return shell; };
	const res = await pcbExport3DShell({});
	assert.equal(res.artifacts?.[0].kind, 'pcb_3d_shell');
	assert.equal(Buffer.from(res.artifacts![0].inlineBase64!, 'base64').toString(), 'solid shell');
	shell = undefined;
	await assert.rejects(pcbExport3DShell({}), /no 3D shell/);
	for (const bad of [{ fileType: 'iges' }, { fileName: '../x' }, { extra: 1 }]) await assert.rejects(pcbExport3DShell(bad));
});
